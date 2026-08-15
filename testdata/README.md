# Test fixtures

These files are **committed on purpose**. They are generated once, by hand, and
checked in: regenerating them at test time would be slower, would make results
depend on whichever version of poppler the machine has, and could not produce
the fixtures that matter most (a PDF with no text layer, an encrypted PDF, a
corrupt PDF).

Regenerate with:

    cd scripts/genfixtures && go run .

Regeneration is deterministic — the generator pins the PDF creation date and
seeds the corruption RNG, so rerunning it on an unchanged tree produces
byte-identical files and no git diff. A diff after a rerun means something
really changed.

`scripts/genfixtures` is a **separate, nested Go module**. Its only dependency,
`github.com/jung-kurt/gofpdf`, therefore never appears in the main module's
`go.mod`, and `go build ./...` / `go vet ./...` at the repo root never see it.
Do not move the generator into the main module: `go mod tidy` keeps any
dependency imported by a non-test package, which is exactly how gofpdf ended up
in the production require block before.

`.gitignore` deliberately does not ignore `testdata/` — `git check-ignore
testdata/*` reports nothing. If a fixture ever vanishes from a checkout, the
tests skip rather than fail, so a missing fixture is silent: check that first
when a fixture test stops running.

## What each fixture is for

| File | What it is | What breaks without it |
| --- | --- | --- |
| `receipt-text.pdf` | One page, real text layer. Vendor `Test Store`, date `2023-01-15`, `Total: $123.45`. | The happy path: `ExtractPDFText` returning usable text, and the fd-leak loop test. |
| `receipt-multipage.pdf` | Three pages, all with text (pages 2 and 3 carry distinct markers). | Multi-page accumulation — the old code returned only page 1's text on any later-page error. |
| `receipt-encrypted.pdf` | `SetProtection(CnProtectPrint, "userpw", "ownerpw")`. Opening with an empty password fails. | `doc.ErrEncrypted`. If this ever opens cleanly the encrypted branch is dead code. |
| `receipt-corrupt.pdf` | `receipt-text.pdf` with 8 deterministic byte flips (seed 20230115) inside the xref region. | `doc.ErrPDFParse` — no panic, no hang. This is the regression guard against the dslipak-style infinite spin; keep it if the PDF library is ever swapped again. |
| `receipt-empty.pdf` | Literally `%PDF-1.4\n%%EOF\n`. | The zero-page / truncated-file branch. |
| `receipt.png`, `receipt.jpg` | 640x440 grayscale images with the same receipt text drawn in a built-in 5x7 bitmap font. | The image path: base64 must be bare (no `data:` prefix) and must decode. Legible enough to feed a vision model by hand. |
| `receipt-scanned.pdf` | `receipt-text.pdf` rasterized at 150 DPI with `pdftoppm` and re-embedded as a PNG. One page, **zero** extractable text. | The whole vision path. |
| `receipt-panics.pdf` | `receipt-text.pdf` with 6 byte flips (seed 105) that make the library *panic* rather than return an error. | `TestExtractPDFTextDoesNotLeakOnPanic`. `pdf.Open` leaks one descriptor per call on this input, which is why the code opens the file itself; the test skips if the input stops panicking, so a replacement must be re-fuzzed rather than hand-edited. |

## receipt-scanned.pdf is the dangerous one

It must have **no text layer**. If a regenerated copy gains one, `doc.Load`
takes the text branch, the rasterizer is never called, and every test still
passes while the entire vision path goes untested. That is why
`internal/doc/doc_test.go` asserts the fixture extracts fewer than
`doc.MinTextChars` non-space runes instead of trusting the file's name.

Verify by hand after regenerating — this must print `0`:

    pdftotext testdata/receipt-scanned.pdf - | tr -d '[:space:]' | wc -c

Regenerating it requires `pdftoppm` (poppler-utils); the generator prints a skip
notice and leaves the committed copy alone when the tool is absent.

## Not generated

No HEIC fixture: there is no CGO-free HEIC encoder, so the `.heic` branch is
covered by a `doc.Load` unit test with a stubbed `Rasterizer` instead. The
oversize-image rejection is likewise exercised with a file the test writes
itself, so nothing multi-megabyte lands in the repo.
