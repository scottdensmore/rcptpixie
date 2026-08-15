package doc

import (
	"context"
	"os"
	"testing"

	"github.com/ledongthuc/pdf"
)

// pdf.Open creates the file and then calls NewReader, which panics on some
// malformed input. The caller never receives the handle, so it leaks — which is
// why ExtractPDFText opens the file itself.
func TestExtractPDFTextDoesNotLeakOnPanic(t *testing.T) {
	const path = "../../testdata/receipt-panics.pdf"
	if _, err := os.Stat(path); err != nil {
		t.Skip("fixture missing")
	}
	fds := func() int {
		ents, err := os.ReadDir("/proc/self/fd")
		if err != nil {
			t.Skip("no /proc/self/fd on this platform")
		}
		return len(ents)
	}

	// Establish the hazard, otherwise a passing test proves nothing.
	base := fds()
	for i := 0; i < 100; i++ {
		func() {
			defer func() { recover() }()
			f, _, err := pdf.Open(path)
			if err == nil && f != nil {
				f.Close()
			}
		}()
	}
	leaked := fds() - base
	if leaked < 10 {
		t.Skipf("pdf.Open no longer leaks on this input (%d fds); nothing to guard", leaked)
	}
	t.Logf("pdf.Open leaks %d descriptors over 100 parses", leaked)

	for i := 0; i < 20; i++ { // settle
		ExtractPDFText(context.Background(), path, 5, nil)
	}
	before := fds()
	for i := 0; i < 300; i++ {
		ExtractPDFText(context.Background(), path, 5, nil)
	}
	if grew := fds() - before; grew > 10 {
		t.Errorf("ExtractPDFText leaked %d descriptors over 300 parses", grew)
	} else {
		t.Logf("ExtractPDFText: %d -> %d descriptors over 300 parses", before, fds())
	}
}
