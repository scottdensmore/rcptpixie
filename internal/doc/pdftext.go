package doc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/ledongthuc/pdf"
)

var (
	ErrEncrypted = errors.New("pdf requires a password")
	ErrPDFParse  = errors.New("malformed pdf")
)

const (
	pdfParseTimeout = 30 * time.Second
	maxPDFTextBytes = 4 << 20
)

type pdfResult struct {
	text  string
	pages int
	err   error
}

// ExtractPDFText returns the concatenated plain text of up to maxPages pages.
// It never panics and never blocks longer than pdfParseTimeout.
func ExtractPDFText(ctx context.Context, path string, maxPages int, log *slog.Logger) (string, int, error) {
	log = orDiscard(log)
	if maxPages <= 0 {
		maxPages = 1
	}

	ch := make(chan pdfResult, 1)
	go func() {
		var res pdfResult
		// Registered first so it runs last and still fires after f.Close().
		defer func() {
			if r := recover(); r != nil {
				res = pdfResult{err: fmt.Errorf("%w %s: %v", ErrPDFParse, path, r)}
			}
			ch <- res
		}()

		// Opened here rather than through pdf.Open: that helper creates the file
		// and then calls NewReader, which panics on some malformed input, leaking
		// the descriptor because the caller never receives it.
		f, err := os.Open(path)
		if err != nil {
			res.err = fmt.Errorf("%w %s: %v", ErrPDFParse, path, err)
			return
		}
		defer f.Close()

		fi, err := f.Stat()
		if err != nil {
			res.err = fmt.Errorf("%w %s: %v", ErrPDFParse, path, err)
			return
		}

		// Everything that touches the library runs inside one withoutStdout: it
		// reports malformed dictionaries and unknown CMap ranges with fmt.Printf,
		// and this command's stdout carries results only. Objects resolve lazily,
		// so NumPage and Page reach those paths just as NewReader does — wrapping
		// only the obvious call is not enough.
		var b strings.Builder
		withoutStdout(func() {
			var rd *pdf.Reader
			rd, err = pdf.NewReader(f, fi.Size())
			if err != nil {
				return
			}
			n := rd.NumPage()
			if n <= 0 {
				return
			}
			res.pages = n

			for i := 1; i <= min(n, maxPages); i++ {
				p := rd.Page(i)
				if p.V.IsNull() {
					continue
				}
				t, perr := p.GetPlainText(nil)
				if perr != nil {
					log.Debug("skipping page", "path", path, "page", i, "err", perr)
					continue
				}
				b.WriteString(t)
				b.WriteString("\n")
				if b.Len() > maxPDFTextBytes {
					break
				}
			}
		})
		if err != nil {
			if errors.Is(err, pdf.ErrInvalidPassword) {
				res.err = fmt.Errorf("%w: %s", ErrEncrypted, path)
			} else {
				res.err = fmt.Errorf("%w %s: %v", ErrPDFParse, path, err)
			}
			return
		}
		res.text = b.String()
	}()

	select {
	case res := <-ch:
		return res.text, res.pages, res.err
	case <-ctx.Done():
		return "", 0, ctx.Err()
	case <-time.After(pdfParseTimeout):
		return "", 0, fmt.Errorf("%w %s: timed out after %s", ErrPDFParse, path, pdfParseTimeout)
	}
}

func orDiscard(log *slog.Logger) *slog.Logger {
	if log != nil {
		return log
	}
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// stdoutMu serialises the os.Stdout swap in withoutStdout. Files are processed
// sequentially, so this only ever contends between parallel tests.
var stdoutMu sync.Mutex

// withoutStdout runs fn with the process stdout pointed at the null device.
// ledongthuc/pdf reports unparseable dictionaries and unknown CMap destinations
// with fmt.Printf, which would otherwise land in the middle of the plan table
// that this command's stdout is reserved for. Writers that captured the real
// *os.File earlier — every one of this command's own — are unaffected.
func withoutStdout(fn func()) {
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		fn()
		return
	}
	stdoutMu.Lock()
	saved := os.Stdout
	os.Stdout = devnull
	defer func() {
		os.Stdout = saved
		stdoutMu.Unlock()
		devnull.Close()
	}()
	fn()
}
