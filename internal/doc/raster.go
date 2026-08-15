package doc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Rasterizer wraps whichever external tool is installed. Everything here shells
// out because a pure-Go PDF renderer does not exist and the cgo ones would break
// CGO_ENABLED=0 builds.
type Rasterizer interface {
	Render(ctx context.Context, pdfPath string, pages int) ([][]byte, error) // JPEG/PNG bytes
	Convert(ctx context.Context, imgPath string) ([]byte, error)             // HEIC/WEBP -> JPEG
	Name() string
}

var (
	ErrNoRasterizer = errors.New("no PDF rasterizer found")
	ErrNoConverter  = errors.New("no image converter found")
)

const (
	toolTimeout   = 60 * time.Second
	maxToolStderr = 64 << 10
	minImageBytes = 1024
	renderWidth   = "1600"
	renderDPI     = "150"
	jpegQuality   = "85"
)

type external struct {
	render  string
	convert string
	log     *slog.Logger
}

var (
	detectOnce sync.Once
	detected   external
)

// Detect probes PATH once and always returns a usable value: with no tool found
// the methods fail with an install hint, so the text-layer path pays nothing.
func Detect(log *slog.Logger) Rasterizer {
	detectOnce.Do(func() {
		detected.render = firstOnPath(renderCandidates())
		detected.convert = firstOnPath(convertCandidates())
	})
	e := detected
	e.log = orDiscard(log)
	e.log.Debug("rasterizer detected", "render", e.render, "convert", e.convert)
	return &e
}

func unavailable() Rasterizer {
	return &external{log: orDiscard(nil)}
}

// renderCandidates deliberately excludes sips. sips reads a PDF without
// complaint and writes an all-white image for it, which the blank check then
// rejects on every scanned file — a Mac with no other tool is better served by
// "install poppler" than by a per-file blank-page error. sips remains a
// converter, which is the job it does well.
func renderCandidates() []string {
	c := []string{"pdftoppm"}
	if runtime.GOOS == "windows" {
		c = append(c, "gswin64c", "gswin32c")
	}
	return append(c, "gs", "magick", "convert")
}

func convertCandidates() []string {
	var c []string
	if runtime.GOOS == "darwin" {
		c = append(c, "sips")
	}
	return append(c, "magick", "convert", "heif-convert")
}

func firstOnPath(names []string) string {
	for _, n := range names {
		if _, err := exec.LookPath(n); err == nil {
			return n
		}
	}
	return ""
}

func (e *external) Name() string {
	switch {
	case e.render != "":
		return e.render
	case e.convert != "":
		return e.convert
	}
	return "none"
}

func (e *external) Render(ctx context.Context, pdfPath string, pages int) ([][]byte, error) {
	if e.render == "" {
		return nil, noRasterizerError()
	}
	if pages <= 0 {
		pages = 1
	}
	// Never write next to the user's file: receipt folders are routinely inside
	// sync roots or on read-only media.
	dir, err := os.MkdirTemp("", "rcptpixie-render-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)

	var out [][]byte
	for p := 1; p <= pages; p++ {
		img, err := e.renderPage(ctx, pdfPath, p, dir)
		if err != nil {
			if p == 1 {
				return nil, err
			}
			e.log.Debug("stopping render", "path", pdfPath, "page", p, "err", err)
			break
		}
		out = append(out, img)
	}
	return out, nil
}

func (e *external) renderPage(ctx context.Context, pdfPath string, page int, dir string) ([]byte, error) {
	base := filepath.Join(dir, fmt.Sprintf("page-%d", page))
	outPath := base + ".png"
	n := strconv.Itoa(page)
	src := toolPath(pdfPath)

	var args []string
	switch e.render {
	case "pdftoppm":
		// -singlefile is required: without it poppler appends a zero-padded page
		// number whose digit count varies with the document's page count.
		args = []string{"-png", "-cropbox", "-scale-to", renderWidth, "-f", n, "-l", n, "-singlefile", src, base}
	case "gs", "gswin64c", "gswin32c":
		args = []string{"-q", "-dNOPAUSE", "-dBATCH", "-dSAFER", "-dFirstPage=" + n, "-dLastPage=" + n,
			"-sDEVICE=png16m", "-r" + renderDPI, "-sOutputFile=" + outPath, src}
	case "magick", "convert":
		args = []string{"-density", renderDPI, fmt.Sprintf("%s[%d]", src, page-1), outPath}
	case "sips":
		if page != 1 {
			return nil, fmt.Errorf("sips can only render page 1 of %s", pdfPath)
		}
		args = []string{"-s", "format", "png", src, "--out", outPath}
	default:
		return nil, noRasterizerError()
	}

	stderr, err := e.run(ctx, e.render, args)
	if err != nil {
		if policyDenied(stderr) {
			return nil, fmt.Errorf("%s refused to read %s: ImageMagick's policy.xml disables the PDF delegate; install poppler or ghostscript instead", e.render, pdfPath)
		}
		return nil, fmt.Errorf("%s failed on %s page %d: %v%s", e.render, pdfPath, page, err, stderrTail(stderr))
	}

	img, err := readValidImage(outPath)
	if err != nil {
		return nil, fmt.Errorf("%s produced no usable image for %s page %d: %v%s", e.render, pdfPath, page, err, stderrTail(stderr))
	}
	// pdftoppm exits 0 on a damaged PDF and still writes a blank page; feeding
	// the model an empty sheet makes it invent a receipt.
	if blankImage(img) {
		return nil, fmt.Errorf("%s rendered %s page %d as a blank page%s", e.render, pdfPath, page, stderrTail(stderr))
	}
	return img, nil
}

func (e *external) Convert(ctx context.Context, imgPath string) ([]byte, error) {
	if e.convert == "" {
		return nil, noConverterError()
	}
	dir, err := os.MkdirTemp("", "rcptpixie-convert-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	outPath := filepath.Join(dir, "converted.jpg")
	src := toolPath(imgPath)

	var args []string
	switch e.convert {
	case "sips":
		args = []string{"-s", "format", "jpeg", "-s", "formatOptions", jpegQuality, src, "--out", outPath}
	case "magick", "convert":
		args = []string{src, "-auto-orient", "-quality", jpegQuality, outPath}
	case "heif-convert":
		args = []string{"-q", jpegQuality, src, outPath}
	default:
		return nil, noConverterError()
	}

	stderr, err := e.run(ctx, e.convert, args)
	if err != nil {
		return nil, fmt.Errorf("%s failed on %s: %v%s", e.convert, imgPath, err, stderrTail(stderr))
	}
	img, err := readValidImage(outPath)
	if err != nil {
		return nil, fmt.Errorf("%s produced no usable image for %s: %v%s", e.convert, imgPath, err, stderrTail(stderr))
	}
	return img, nil
}

// toolPath makes a path safe to hand to a tool as a bare argument. No shell is
// ever involved (see run), so only a leading "-" is dangerous: ghostscript reads
// "-scan.pdf" as an unknown switch, renders nothing and still exits 0. None of
// the tools agree on a "--" end-of-options marker, so the fix is the path
// itself. The prefix also keeps a colon in the name away from ImageMagick's
// "coder:file" parsing.
func toolPath(p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return "." + string(filepath.Separator) + p
}

func (e *external) run(ctx context.Context, name string, args []string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, toolTimeout)
	defer cancel()

	e.log.Debug("running", "tool", name, "args", args)
	stderr := &boundedBuffer{limit: maxToolStderr}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = io.Discard
	cmd.Stderr = stderr
	err := cmd.Run()
	if err != nil && ctx.Err() != nil {
		err = fmt.Errorf("timed out after %s", toolTimeout)
	}
	return stderr.String(), err
}

// boundedBuffer keeps at most limit bytes; a corrupt PDF emits unbounded
// "Syntax Error" lines.
type boundedBuffer struct {
	buf   bytes.Buffer
	limit int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if room := b.limit - b.buf.Len(); room > 0 {
		if len(p) > room {
			p = p[:room]
		}
		b.buf.Write(p)
	}
	return len(p), nil
}

func (b *boundedBuffer) String() string { return b.buf.String() }

func stderrTail(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) > 3 {
		lines = lines[len(lines)-3:]
	}
	return ": " + strings.Join(lines, "; ")
}

func policyDenied(stderr string) bool {
	return strings.Contains(strings.ToLower(stderr), "not authorized")
}

// readValidImage enforces that the tool actually produced an image: an exit code
// of 0 is not a validity signal.
func readValidImage(path string) ([]byte, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("no output file: %v", err)
	}
	if fi.Size() < minImageBytes {
		return nil, fmt.Errorf("output is only %d bytes", fi.Size())
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("output is not a decodable image: %v", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, fmt.Errorf("output has zero dimensions")
	}
	return b, nil
}

// blankImage reports whether every sampled pixel has the same colour. Rows are
// scanned whole so a page with a single line of text is never called blank.
func blankImage(b []byte) bool {
	img, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return false
	}
	rect := img.Bounds()
	if rect.Dx() < 2 || rect.Dy() < 2 {
		return true
	}
	step := max(1, rect.Dy()/400)
	r0, g0, b0, _ := img.At(rect.Min.X, rect.Min.Y).RGBA()
	for y := rect.Min.Y; y < rect.Max.Y; y += step {
		for x := rect.Min.X; x < rect.Max.X; x++ {
			r, g, bb, _ := img.At(x, y).RGBA()
			if r != r0 || g != g0 || bb != b0 {
				return false
			}
		}
	}
	return true
}

func noRasterizerError() error {
	return fmt.Errorf("%w: PDF has no text layer and no rasterizer was found.\n  Install poppler: %s\n  (Ghostscript also works.)",
		ErrNoRasterizer, popplerHint())
}

func noConverterError() error {
	return fmt.Errorf("%w: HEIC cannot be decoded without a converter.\n  %s",
		ErrNoConverter, converterHint())
}

func popplerHint() string {
	switch runtime.GOOS {
	case "darwin":
		return "brew install poppler"
	case "windows":
		return "winget install oschwartz10612.Poppler"
	}
	return "sudo apt install poppler-utils"
}

func converterHint() string {
	if runtime.GOOS == "darwin" {
		return "macOS has sips built in; if it is missing: brew install imagemagick"
	}
	return "sudo apt install imagemagick (or libheif-examples)"
}
