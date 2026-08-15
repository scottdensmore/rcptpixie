// Package doc turns an input file into model-ready content: extracted text, or
// base64-encoded page images when there is no text to extract.
package doc

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type Kind int

const (
	KindText Kind = iota
	KindImages
)

type Doc struct {
	Path    string
	Kind    Kind
	Text    string   // KindText, already truncated
	Images  []string // KindImages: bare StdEncoding base64, no data: prefix
	Pages   int
	ModTime time.Time
}

const (
	MaxTextChars  = 12000   // ~3-4k tokens, comfortably inside num_ctx 8192
	MaxImageBytes = 8 << 20 // guard against posting a 40MB scan
	MinTextChars  = 64      // below this a PDF is treated as a scan
)

// visionPages is how many pages are rendered when a PDF has no text layer.
const visionPages = 2

// textPages is how many pages are read for text: page 1 has the vendor and the
// date, page 2 usually has the total on an itemised invoice.
const textPages = 3

var ImageExts = []string{".jpg", ".jpeg", ".png", ".heic", ".heif", ".webp"}

var ErrUnsupported = errors.New("unsupported file type")

// Load reads path and returns either text or page images.
func Load(ctx context.Context, path string, r Rasterizer, log *slog.Logger) (*Doc, error) {
	log = orDiscard(log)
	if r == nil {
		r = unavailable()
	}
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	d := &Doc{Path: path, ModTime: fi.ModTime()}

	ext := strings.ToLower(filepath.Ext(path))
	switch {
	case ext == ".pdf":
		return loadPDF(ctx, d, r, log)
	case slices.Contains(ImageExts, ext):
		return loadImage(ctx, d, ext, fi.Size(), r, log)
	case ext == ".txt" || ext == ".md":
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		d.Kind = KindText
		d.Pages = 1
		d.Text, _ = Truncate(string(b), MaxTextChars)
		log.Debug("loaded", "path", path, "via", "plain text", "chars", len(d.Text))
		return d, nil
	}
	return nil, fmt.Errorf("%w: %s", ErrUnsupported, ext)
}

func loadPDF(ctx context.Context, d *Doc, r Rasterizer, log *slog.Logger) (*Doc, error) {
	text, pages, err := ExtractPDFText(ctx, d.Path, textPages, log)
	switch {
	case errors.Is(err, ErrEncrypted):
		return nil, err
	case err != nil:
		log.Debug("pdf text extraction failed", "path", d.Path, "err", err)
		if rerr := renderInto(ctx, d, r, log); rerr != nil {
			return nil, err
		}
		return d, nil
	}

	if usableText(text) {
		d.Kind = KindText
		d.Pages = pages
		d.Text, _ = Truncate(text, MaxTextChars)
		log.Debug("loaded", "path", d.Path, "via", "pdf text", "pages", pages, "chars", len(d.Text))
		return d, nil
	}

	log.Debug("pdf has no usable text layer, rendering", "path", d.Path)
	if err := renderInto(ctx, d, r, log); err != nil {
		return nil, err
	}
	return d, nil
}

func renderInto(ctx context.Context, d *Doc, r Rasterizer, log *slog.Logger) error {
	imgs, err := r.Render(ctx, d.Path, visionPages)
	if err != nil {
		return err
	}
	if len(imgs) == 0 {
		return fmt.Errorf("%s rendered no pages of %s", r.Name(), d.Path)
	}
	for _, img := range imgs {
		if len(img) > MaxImageBytes {
			return fmt.Errorf("rendered page of %s is %d bytes, over the %d byte limit", d.Path, len(img), MaxImageBytes)
		}
		d.Images = append(d.Images, base64.StdEncoding.EncodeToString(img))
	}
	d.Kind = KindImages
	d.Pages = len(d.Images)
	log.Debug("loaded", "path", d.Path, "via", "rasterizer", "tool", r.Name(), "pages", d.Pages)
	return nil
}

func loadImage(ctx context.Context, d *Doc, ext string, size int64, r Rasterizer, log *slog.Logger) (*Doc, error) {
	var (
		b   []byte
		err error
		via string
	)
	switch ext {
	case ".heic", ".heif", ".webp":
		// Ollama's decoder rejects these, so they need a hop through a converter.
		if b, err = r.Convert(ctx, d.Path); err != nil {
			return nil, err
		}
		via = r.Name()
	default:
		if size > MaxImageBytes {
			return nil, fmt.Errorf("%s is %d bytes, over the %d byte limit", d.Path, size, MaxImageBytes)
		}
		if b, err = os.ReadFile(d.Path); err != nil {
			return nil, err
		}
		via = "direct"
	}
	if len(b) > MaxImageBytes {
		return nil, fmt.Errorf("%s is %d bytes, over the %d byte limit", d.Path, len(b), MaxImageBytes)
	}

	d.Kind = KindImages
	d.Pages = 1
	d.Images = []string{base64.StdEncoding.EncodeToString(b)}
	log.Debug("loaded", "path", d.Path, "via", via, "bytes", len(b))
	return d, nil
}

// usableText rejects both a missing text layer and the long runs of raw font
// bytes that subset fonts without a /ToUnicode map decode into.
func usableText(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	n := 0
	for _, r := range s {
		if !unicode.IsSpace(r) {
			n++
		}
	}
	return n >= MinTextChars
}

// Truncate keeps the head and the tail of s. Head-biased because the vendor,
// address and date are at the top; the tail is kept because the TOTAL is at the
// bottom and a head-only cut deletes exactly the field the tool exists to
// extract. The result is always valid UTF-8 and never longer than limit.
func Truncate(s string, limit int) (string, bool) {
	if limit <= 0 {
		return "", s != ""
	}
	if len(s) <= limit {
		return strings.ToValidUTF8(s, ""), false
	}

	budget := limit - len(omissionMarker(len(s)))
	if budget < 2 {
		return strings.ToValidUTF8(headSlice(s, limit), ""), true
	}
	head := headSlice(s, budget*7/10)
	tail := tailSlice(s, budget-budget*7/10)
	omitted := len(s) - len(head) - len(tail)
	if omitted <= 0 {
		return strings.ToValidUTF8(s, ""), false
	}
	return strings.ToValidUTF8(head+omissionMarker(omitted)+tail, ""), true
}

func omissionMarker(n int) string {
	return fmt.Sprintf("\n[... %d characters omitted ...]\n", n)
}

// headSlice cuts at the last line break in the final quarter of the window, so
// at least three quarters of the budget is always used.
func headSlice(s string, n int) string {
	if n >= len(s) {
		return s
	}
	seg := s[:n]
	if i := strings.LastIndexByte(seg, '\n'); i >= n*3/4 {
		return seg[:i+1]
	}
	return seg
}

// tailSlice cuts at the first line break in the first quarter of the window, so
// the last characters of s always survive.
func tailSlice(s string, n int) string {
	if n >= len(s) {
		return s
	}
	seg := s[len(s)-n:]
	if i := strings.IndexByte(seg, '\n'); i >= 0 && i <= n/4 {
		return seg[i+1:]
	}
	// A rune split by the cut is dropped by the ToValidUTF8 pass in Truncate.
	return seg
}

// IsSupported reports whether path's extension is in exts. A nil exts means
// every regular file, which is organize mode's default.
func IsSupported(path string, exts []string) bool {
	if len(exts) == 0 {
		return true
	}
	ext := strings.ToLower(filepath.Ext(path))
	for _, e := range exts {
		if ext == strings.ToLower(e) {
			return true
		}
	}
	return false
}
