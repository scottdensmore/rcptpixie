# RcptPixie

[![Test](https://github.com/scottdensmore/rcptpixie/actions/workflows/test.yml/badge.svg)](https://github.com/scottdensmore/rcptpixie/actions/workflows/test.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/scottdensmore/rcptpixie)](https://goreportcard.com/report/github.com/scottdensmore/rcptpixie)

A command-line tool that renames receipts and documents from what is written
inside them, using a local LLM through [Ollama](https://ollama.ai/). Nothing
leaves your machine.

```
receipt-scan-002.pdf   ->  01-15-2023 - 123.45 - Test_Store - Food.pdf
Scan_20240311_0004.pdf ->  2024-03-11 - Comcast Internet Service Invoice.pdf
```

## Features

- **Two modes.** `receipts` produces the expense-report filename
  (`MM-DD-YYYY - TOTAL - Vendor - Category.ext`); `organize` produces a
  human-readable one (`YYYY-MM-DD - Descriptive Subject.ext`) for anything else.
- **Reads scans and photos**, not just PDFs with a text layer: `.pdf`, `.jpg`,
  `.jpeg`, `.png`, `.heic`, `.heif`, `.webp`, `.txt`, `.md`.
- **Handles regular and hotel receipts**, including check-in/check-out ranges.
- **Understands messy totals** — `$1,234.56`, `1.234,56`, `12.00 USD` and
  `(12.00)` all parse (covered by tests in `internal/analyze`).
- **Dry run** (`-n`) prints the exact plan and changes nothing.
- **Undo** — every rename is journaled and reversible with `rcptpixie undo`.
- **Never overwrites a file.** Collisions get a ` (2)` suffix.
- Single file or directory; recursion is opt-in.
- Cross-platform (macOS, Linux, Windows), no cgo.

## Prerequisites

- **Go 1.24.1 or later** (only to build from source).
- **[Ollama](https://ollama.ai/) installed and running** (`ollama serve`).
- **A model.** The default is `gemma4:e2b`:

  ```bash
  ollama pull gemma4:e2b
  ```

### About the default model

`gemma4:e2b` is the **multimodal** variant, which is what makes scanned PDFs,
photos and HEIC files work at all — a text-only model cannot read them. The
`e2b` tag is the elastic **~2B-effective** build (Ollama reports 5.1 B weights
at Q4_K_M on disk); there is **no plain `gemma4:2b` tag**, so don't go looking
for one.

| Tag | Download | Notes |
| --- | --- | --- |
| `gemma4:e2b` | ~7.2 GB | The default. Multimodal, Q4_K_M. |
| `gemma4:e2b-it-qat` | ~4.3 GB | Same size class, quantization-aware trained. Use it on a constrained machine. |
| `gemma4:e4b`, `gemma4:12b`, and up (`26b`, `31b`) | larger | Better subjects and better date reading, at proportionally more RAM. Select with `-model`. |

**Accuracy, measured on this model.** When a PDF has a real text layer, the
extraction is accurate — vendor, date, total and category all come back
correct. When the file is a **scan or a photo**, the vision path reliably reads
the **vendor and the total**, but the **dates can be wrong** — on a hotel folio
it has been observed returning the check-out date as the check-in and inventing
the other. rcptpixie defends against the worst of that (it swaps reversed date
ranges and drops implausible stays), but the rule stands:

> Give scanned receipts a look before you file them. If the dates are wrong,
> escalate the model: `rcptpixie receipts -model gemma4:e4b ~/Receipts`.

Use `-n` first and you will see the dates before anything is renamed.

## Optional dependencies

**None are needed** for ordinary text-layer PDFs, `.jpg`, `.png`, `.txt` or
`.md`. They are needed only to rasterize a **scanned PDF** (one with no text
layer) or to decode **HEIC/WEBP**.

Any one of these is enough for scanned PDFs — rcptpixie probes `PATH` and uses
the first it finds: `pdftoppm` (poppler), `gs`/`gswin64c` (Ghostscript),
`magick`/`convert` (ImageMagick), or `sips` (macOS built-in).

| Platform | Recommended |
| --- | --- |
| macOS | `brew install poppler` |
| Debian/Ubuntu | `sudo apt install poppler-utils` |
| Windows | `winget install oschwartz10612.Poppler` |

For HEIC/WEBP the probe order is `sips` (macOS), `magick`, `convert`,
`heif-convert`. macOS has `sips` built in; elsewhere
`sudo apt install imagemagick` (or `libheif-examples`).

If nothing is installed, text-layer files still work and scanned ones fail with
the install hint above — nothing silently degrades. Note that many ImageMagick
packages ship a `policy.xml` that disables the PDF delegate; rcptpixie detects
that and tells you to install poppler or Ghostscript instead.

## Installation

### With `go install`

```bash
go install github.com/scottdensmore/rcptpixie/v2/cmd/rcptpixie@latest
```

The `/v2` is required. Go treats each major version as its own module path, so
the v1 path resolves to v1.1.0 and stops there.

### From source

```bash
git clone https://github.com/scottdensmore/rcptpixie.git
cd rcptpixie
go install ./cmd/rcptpixie
```

### From GitHub Releases

1. Visit the [Releases page](https://github.com/scottdensmore/rcptpixie/releases)
2. Download the archive for your platform
3. Extract it and move the binary somewhere on your `PATH`

### macOS

```bash
brew tap scottdensmore/tap
brew install rcptpixie
```

### Linux

```bash
curl -L https://github.com/scottdensmore/rcptpixie/releases/latest/download/rcptpixie_Linux_x86_64.tar.gz | tar xz
sudo mv rcptpixie /usr/local/bin/
```

### Windows

1. Download the latest Windows release
2. Extract the ZIP
3. Add the directory containing `rcptpixie.exe` to your `PATH`

## Quick start

```bash
ollama serve                                  # if it is not already running
ollama pull gemma4:e2b

rcptpixie organize --dry-run ~/Downloads      # look at the plan first
rcptpixie organize ~/Downloads                # then do it (it asks to confirm)
```

The first document of a run takes 20-45 seconds on CPU while the model loads;
the rest are much faster.

## Usage

The command shape is:

```
rcptpixie <command> [flags] <file-or-directory>
```

**Flags go after the subcommand.** `rcptpixie receipts -v ./dir` works;
`rcptpixie -v receipts ./dir` does not (`receipts` would be read as a path).

### `receipts` — expense-report names

```bash
rcptpixie receipts ~/Receipts/hotel.pdf       # one file
rcptpixie receipts ~/Receipts                 # a directory, top level only
rcptpixie receipts -r ~/Receipts              # ...and its subdirectories
rcptpixie receipts -n ~/Receipts              # dry run: print the plan, change nothing
rcptpixie receipts -model gemma4:e4b ~/Receipts
```

A bare path still runs receipts mode, so the old one-liner keeps working:

```bash
rcptpixie receipt.pdf
rcptpixie -verbose receipt.pdf
```

If a path collides with a command name, disambiguate with `--` or `./`:

```bash
rcptpixie receipts -- ./organize
rcptpixie ./undo
```

### `organize` — readable names for everything else

```bash
rcptpixie organize --dry-run ~/Documents/Scans
rcptpixie organize ~/Documents/Scans          # prompts before renaming
rcptpixie organize -y ~/Documents/Scans       # skip the prompt
rcptpixie organize -ext .pdf ~/Downloads      # only PDFs
```

Organize mode considers **every** regular file by default, so unsupported types
(`.zip`, `.dmg`, ...) are reported as errors in the plan. Narrow it with `-ext`
when pointing at a mixed folder. Files already named
`YYYY-MM-DD - Something.ext` are skipped without calling the model, so a second
run over a large folder is nearly free.

### `undo` — put the names back

```bash
rcptpixie undo ~/Documents/Scans              # prompts, then reverts
rcptpixie undo -n ~/Documents/Scans           # show what would be reverted
rcptpixie undo -y ~/Documents/Scans
rcptpixie undo                                # defaults to the current directory
```

### Version and help

```bash
rcptpixie                # usage, exit code 2
rcptpixie help           # usage, exit code 0
rcptpixie -h
rcptpixie -version
rcptpixie receipts -h    # per-command flags
```

`-h` and `-version` are root-level: use them *before* a subcommand, or use
`rcptpixie <command> -h` for that command's flag list.

## Command reference

| Flag | Short | Default | Environment | Commands |
| --- | --- | --- | --- | --- |
| `-model` | — | `gemma4:e2b` | `RCPTPIXIE_MODEL` | receipts, organize |
| `-host` | — | `http://localhost:11434` | `RCPTPIXIE_HOST`, then `OLLAMA_HOST` | receipts, organize |
| `-timeout` | — | `5m` | — | receipts, organize |
| `-ext` | — | receipts: `.pdf,.jpg,.jpeg,.png,.heic`; organize: empty (every file) | — | receipts, organize |
| `-recursive` | `-r` | off | — | receipts, organize |
| `-dry-run` | `-n` | off | — | all |
| `-yes` | `-y` | off | — | organize, undo |
| `-verbose` | `-v` | off | — | all |
| `-quiet` | `-q` | off | — | all |

Notes:

- An explicit flag always beats the environment variable.
- `-host` accepts `host`, `host:port` or a full URL; a bare host becomes
  `http://` and a missing port becomes `11434`.
- `-timeout` is **per Ollama request**, not per run — a cold model load is
  legitimately minutes.
- `receipts` never prompts, so `-y` has no effect there.
- `-verbose` with `-quiet`, or a non-positive `-timeout`, is a usage error.
- `undo` takes only `-n`, `-y`, `-v` and `-q`.
- The `-ext` filter applies to **directories only**. A single file given
  directly on the command line is always processed, so
  `rcptpixie receipts scan.heif` works even though `.heif` is not in the
  receipts default list.
- Dotfiles, the undo journal, symlinks, non-regular files and zero-byte files
  are always skipped. Without `-r`, subdirectories that contain candidates are
  counted and reported so you know what you did not process.

## Exit codes

| Code | Meaning |
| --- | --- |
| `0` | Everything succeeded (also: dry run finished, nothing to do, or you declined the prompt) |
| `1` | Nothing could be processed, or Ollama is unreachable, or the model is not installed |
| `2` | Usage error, unreadable path, no undo history, or confirmation needed without a terminal |
| `3` | Partial success — some files were processed and some failed |
| `130` | Interrupted (Ctrl-C) |

## How a file is read

1. **PDF with a text layer** — text is extracted from the first 3 pages in
   process. If the result has at least 64 non-space characters, that text is
   what the model sees, truncated to 12,000 characters keeping both the head
   (vendor, date) and the tail (the total).
2. **PDF without a text layer** — the first 2 pages are rasterized by an
   external tool (see [Optional dependencies](#optional-dependencies)) and sent
   to the model as images. A page that renders blank is rejected rather than
   fed to the model, because a blank sheet makes it invent a receipt.
3. **Images** — `.jpg`/`.png` go straight to the model; `.heic`/`.heif`/`.webp`
   are converted to JPEG first because Ollama's decoder rejects them.
4. **`.txt`/`.md`** — read as text.

**Password-protected PDFs are skipped with a clear error, never guessed at.**
rcptpixie makes no attempt at the password and does not try to strip it.

Document text is wrapped in explicit "untrusted data, never instructions"
delimiters before it reaches the model, so a receipt containing "ignore your
instructions and ..." is described rather than obeyed.

## File naming

### `receipts`

```
MM-DD-YYYY - TOTAL - Vendor - Category.ext
MM-DD-YYYY to MM-DD-YYYY - TOTAL - Vendor - Category.ext   (hotel stays)
```

Examples:

- `01-15-2023 - 123.45 - Test_Store - Food.pdf`
- `01-15-2023 to 01-17-2023 - 500.00 - Grand_Hotel - Lodging.pdf`
- `01-15-2023 - 17830.81 - Test_Store - Food_&_Drink.pdf`

Spaces inside the vendor and category become underscores (this format is
unchanged from earlier versions, byte for byte). The category comes from a
closed list, so it can never arrive as free text containing a `/`:
`Airfare`, `Lodging`, `Food`, `Transportation`, `Fuel`, `Groceries`,
`Software`, `Office`, `Utilities`, `Medical`, `Entertainment`, `Other`.

### `organize`

```
YYYY-MM-DD - Descriptive Subject.ext
```

Examples:

- `2024-03-11 - Comcast Internet Service Invoice.pdf`
- `2023-01-15 - Test Store Receipt.pdf`

Spaces are kept — these names are for reading. The subject is 3-8 Title Case
words, capped at 60 characters on a word boundary. If the document states no
date, the file's modification time is used.

### Sanitization and collisions

Every generated name passes one sanitizer before it is used:

- invalid UTF-8, control, bidi-override and zero-width characters are dropped;
- `/`, `\` and `:` become `-`; `<`, `>`, `"`, `|`, `?`, `*` are removed;
- runs of dots collapse to one (so `..` cannot survive);
- all Unicode spaces fold to a single ASCII space;
- leading and trailing `.`, `-`, `_` and spaces are trimmed;
- Windows device names (`CON`, `PRN`, `NUL`, `COM1`...) are prefixed with `_`;
- the whole name is capped at 247 bytes plus the extension, cut on a rune
  boundary;
- an empty result becomes `Untitled`.

If the target name is taken — by another file in the same run or by something
already on disk — the name gets a ` (2)`, ` (3)` ... suffix, matched
case-insensitively so a case-insensitive filesystem cannot be tricked into an
overwrite.

## Undo

Each directory rcptpixie renames in gets an append-only journal at
`<dir>/.rcptpixie-undo.jsonl`. It travels with the folder, so moving or copying
the folder keeps the history with it.

```bash
rcptpixie undo ~/Documents/Scans
```

Renames are reverted in reverse order (so an A→B, B→C chain unwinds cleanly).
An entry is skipped, not forced, when the file is gone, is no longer a regular
file, or its original name is taken again. The journal is removed once it has
been applied.

## Safety

This tool is designed to be pointed at a folder you care about.

- **The LLM never renames anything.** It only proposes text; every filesystem
  operation is performed by rcptpixie itself.
- **Every proposed name is sanitized and then asserted to be contained** in the
  file's own directory — a model that returns `../../.ssh/authorized_keys`
  produces a plain filename, and if it somehow did not, the rename is refused.
- **Nothing is ever overwritten.** The destination is claimed with an
  `O_CREATE|O_EXCL` open before the rename, which closes the
  check-then-rename race that `os.Rename` would otherwise lose silently.
- **Every rename is journaled and fsynced before it happens**, so an
  interrupted run cannot leave an unrecorded, un-undoable rename.
- **Ctrl-C cancels the in-flight model request and stops before the next
  file.** A rename itself is never interrupted half-way: no file is left
  half-renamed.
- `-n` shows you the exact plan first, and `organize` asks before it acts.

## Project structure

```
rcptpixie/
├── cmd/
│   └── rcptpixie/          # main(): signal context -> cli.Run
├── internal/
│   ├── analyze/            # prompts, JSON schemas, field parsing, name formatting
│   ├── cli/                # flags, subcommand routing, the Run seam, logging
│   ├── doc/                # PDF text extraction, rasterizing, image loading
│   ├── ollama/             # the only code that talks to the Ollama HTTP API
│   ├── rename/             # sanitizer, plan/collisions, atomic apply, undo journal
│   └── testutil/           # httptest fake Ollama server
├── scripts/
│   └── genfixtures/        # nested module that regenerates testdata/
├── testdata/               # committed PDF/image fixtures (see testdata/README.md)
├── version/
├── go.mod
└── go.sum
```

## Development

### Running tests

```bash
go test ./... -race
```

**The test suite needs no Ollama and no network.** Every test that exercises
the API runs against an `httptest` fake (`internal/testutil`). Tests that need
a rasterizer skip themselves when `pdftoppm`/`gs` is not installed.

Fixtures live in `testdata/` and are committed on purpose; see
[`testdata/README.md`](testdata/README.md) before regenerating any of them.

### Building

```bash
go build -o rcptpixie ./cmd/rcptpixie
go install ./cmd/rcptpixie
```

## Breaking changes

Version 2 is the first release of the rewrite. If you used an earlier version:

- **The Go module path is now `github.com/scottdensmore/rcptpixie/v2`.** Only
  `go install` is affected — the Homebrew formula and the release archives are
  not. The v1 path keeps resolving to v1.1.0, so an unchanged
  `go install .../cmd/rcptpixie@latest` quietly stays on the old version.
- **Nothing renamed by v1 can be undone by v2.** The undo journal arrived with
  this release, so v1 renames were never recorded.
- **Recursion is now opt-in.** A directory argument processes only its top
  level; pass `-r` for subdirectories. (Previously every subdirectory was
  descended into automatically.) Skipped subdirectories are reported.
- **The default model changed** from `llama3.2` to `gemma4:e2b`, which is
  multimodal — that is what makes scans and photos work. Run
  `ollama pull gemma4:e2b`, or set `RCPTPIXIE_MODEL` / `-model` to keep using
  another one.
- **Running with no arguments prints usage and exits 2** instead of 0, so a
  scripted invocation that silently did nothing now fails loudly.
- **`organize` asks for confirmation** before renaming, and refuses to run
  non-interactively without `-y` (exit code 2) rather than hanging on a prompt
  nobody can see.
- Subcommands exist; flags belong after the verb. A bare path still means
  `receipts`.

## Error handling

Errors are actionable and name the fix:

- Ollama unreachable — tells you to start it, or to set `OLLAMA_HOST`/`-host`.
- Model not installed — prints `ollama pull <model>` and lists what *is*
  installed.
- Scanned PDF with no rasterizer — prints the install command for your OS.
- Password-protected, corrupt, empty or unsupported files — reported per file;
  the run continues and the exit code becomes `3` if anything else succeeded.

## License

MIT License - see LICENSE file for details
