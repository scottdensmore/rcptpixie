package doc

import (
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestToolPath(t *testing.T) {
	t.Parallel()

	// filepath.Join("/", ...) is NOT absolute on Windows — an absolute path there
	// needs a drive letter — so build a real one and keep the rooted form as its
	// own case.
	abs, err := filepath.Abs(filepath.Join("tmp", "-scan.pdf"))
	if err != nil {
		t.Fatalf("Abs: %v", err)
	}
	rooted := filepath.Join(string(filepath.Separator), "tmp", "-scan.pdf")
	dot := "." + string(filepath.Separator)

	tests := []struct{ in, want string }{
		{"-scan.pdf", dot + "-scan.pdf"},
		{"receipt.pdf", dot + "receipt.pdf"},
		{filepath.Join("sub", "-x.pdf"), dot + filepath.Join("sub", "-x.pdf")},
		{abs, abs},
		{rooted, rooted},
		{"", ""},
	}
	for _, tt := range tests {
		if got := toolPath(tt.in); got != tt.want {
			t.Errorf("toolPath(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// writeStub installs a tiny stand-in for an external tool on PATH. The stubs
// reproduce the argument handling of the real binaries: ghostscript silently
// treats "-scan.pdf" as an unknown switch and exits 0 having rendered nothing,
// ImageMagick rejects it outright.
func writeStub(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
}

const gsStub = `
out=
input=
for a in "$@"; do
  case "$a" in
    -sOutputFile=*) out=${a#-sOutputFile=} ;;
    -*) ;;
    *) input=$a ;;
  esac
done
[ -n "$input" ] || exit 0
[ -n "$out" ] || exit 0
cp "$RCPTPIXIE_TEST_PNG" "$out"
`

const magickStub = `
input=
out=
while [ $# -gt 0 ]; do
  case "$1" in
    -density|-quality) shift 2; continue ;;
    -auto-orient) shift; continue ;;
    -*) echo "magick: unrecognized option '$1'" >&2; exit 1 ;;
  esac
  if [ -z "$input" ]; then input=$1; else out=$1; fi
  shift
done
[ -n "$out" ] || { echo "magick: missing output" >&2; exit 1; }
cp "$RCPTPIXIE_TEST_PNG" "$out"
`

// stubTools puts the stand-ins on PATH and chdirs into a fresh directory so the
// paths under test are relative, the way filepath.Join(".", name) leaves them.
func stubTools(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("stubs are /bin/sh scripts")
	}

	binDir := t.TempDir()
	writeStub(t, binDir, "gs", gsStub)
	writeStub(t, binDir, "magick", magickStub)

	work := t.TempDir()
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("RCPTPIXIE_TEST_PNG", writeTestPNG(t, filepath.Join(binDir, "fixture.png")))
	t.Chdir(work)
	return work
}

func writeTestPNG(t *testing.T, path string) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 256, 256))
	for y := 0; y < 256; y++ {
		for x := 0; x < 256; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: uint8(x ^ y), A: 0xff})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestRenderLeadingDashPath is the regression test for a receipt named
// "-scan.pdf": passed bare it is eaten as a switch, and the failure surfaces as
// a misleading "produced no usable image".
func TestRenderLeadingDashPath(t *testing.T) {
	stubTools(t)

	for _, tool := range []string{"gs", "magick"} {
		t.Run(tool, func(t *testing.T) {
			for _, name := range []string{"-scan.pdf", "scan.pdf"} {
				if err := os.WriteFile(name, []byte("%PDF-1.4\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				e := &external{render: tool, log: orDiscard(nil)}
				imgs, err := e.Render(context.Background(), name, 1)
				if err != nil {
					t.Fatalf("%s Render(%q): %v", tool, name, err)
				}
				if len(imgs) != 1 {
					t.Fatalf("%s Render(%q) returned %d images, want 1", tool, name, len(imgs))
				}
			}
		})
	}
}

func TestConvertLeadingDashPath(t *testing.T) {
	stubTools(t)

	for _, name := range []string{"-photo.heic", "photo.heic"} {
		if err := os.WriteFile(name, []byte("heic"), 0o644); err != nil {
			t.Fatal(err)
		}
		e := &external{convert: "magick", log: orDiscard(nil)}
		out, err := e.Convert(context.Background(), name)
		if err != nil {
			t.Fatalf("Convert(%q): %v", name, err)
		}
		if len(out) < minImageBytes {
			t.Errorf("Convert(%q) returned %d bytes", name, len(out))
		}
	}
}

// TestRenderLeadingDashRealGhostscript runs the fix against the binary that
// actually mis-parses the name, when it is installed.
func TestRenderLeadingDashRealGhostscript(t *testing.T) {
	gs := ""
	for _, c := range []string{"gs", "gswin64c", "gswin32c"} {
		if _, err := exec.LookPath(c); err == nil {
			gs = c
			break
		}
	}
	if gs == "" {
		t.Skip("no ghostscript on PATH")
	}
	src := filepath.Join("..", "..", "testdata", "receipt-scanned.pdf")
	pdf, err := os.ReadFile(src)
	if err != nil {
		t.Skipf("fixture %s missing: %v", src, err)
	}

	t.Chdir(t.TempDir())
	if err := os.WriteFile("-scan.pdf", pdf, 0o644); err != nil {
		t.Fatal(err)
	}

	e := &external{render: gs, log: orDiscard(nil)}
	imgs, err := e.Render(context.Background(), "-scan.pdf", 1)
	if err != nil {
		t.Fatalf("%s Render(-scan.pdf): %v", gs, err)
	}
	if len(imgs) != 1 {
		t.Fatalf("%s rendered %d pages, want 1", gs, len(imgs))
	}
}

// TestRenderShellMetacharactersInName is the other half of the argv contract: no
// shell is involved, so a name full of metacharacters is passed through intact.
func TestRenderShellMetacharactersInName(t *testing.T) {
	stubTools(t)

	name := "a b;$(touch pwned)`x`'q\".pdf"
	if err := os.WriteFile(name, []byte("%PDF-1.4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e := &external{render: "gs", log: orDiscard(nil)}
	if _, err := e.Render(context.Background(), name, 1); err != nil {
		t.Fatalf("Render(%q): %v", name, err)
	}
	if _, err := os.Stat("pwned"); err == nil {
		t.Fatal("the name was expanded by a shell")
	}
}
