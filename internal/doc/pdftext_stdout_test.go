package doc

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ledongthuc/pdf"
)

// capture swaps os.Stdout for a pipe and returns whatever fn wrote to it.
func capture(t *testing.T, fn func()) string {
	t.Helper()
	saved := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() { b, _ := io.ReadAll(r); done <- string(b) }()
	fn()
	os.Stdout = saved
	w.Close()
	out := <-done
	r.Close()
	return out
}

func lexTripper(t *testing.T) string {
	t.Helper()
	src, err := os.ReadFile("../../testdata/receipt-text.pdf")
	if err != nil {
		t.Skip("fixture missing")
	}
	// A dictionary KEY that is not a name is what reaches lex.go's fmt.Printf.
	// The replacement is the same length as the original so every xref offset
	// still resolves and the parser actually reaches the dictionary.
	bad := strings.Replace(string(src), "/Type /Catalog", "R /Ty /Catalog", 1)
	p := filepath.Join(t.TempDir(), "lex.pdf")
	if err := os.WriteFile(p, []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// The library prints diagnostics with fmt.Printf; stdout carries results only.
func TestExtractPDFTextWritesNothingToStdout(t *testing.T) {
	path := lexTripper(t)

	// Establish the hazard is real, otherwise this test proves nothing.
	raw := capture(t, func() {
		defer func() { recover() }()
		f, r, err := pdf.Open(path)
		if err != nil {
			return
		}
		defer f.Close()
		for i := 1; i <= r.NumPage(); i++ {
			if p := r.Page(i); !p.V.IsNull() {
				p.GetPlainText(nil)
			}
		}
	})
	if raw == "" {
		t.Skip("this fixture no longer trips the library's fmt.Printf paths")
	}
	t.Logf("library writes %d bytes to stdout unguarded: %q", len(raw), truncate(raw))

	got := capture(t, func() {
		ExtractPDFText(context.Background(), path, 5, nil)
	})
	if got != "" {
		t.Errorf("ExtractPDFText leaked %d bytes to stdout: %q", len(got), truncate(got))
	}
}

func truncate(s string) string {
	if len(s) > 80 {
		return s[:80] + "..."
	}
	return s
}
