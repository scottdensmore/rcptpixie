// Package cli is rcptpixie's command line: flag parsing, subcommand routing and
// the Run seam that lets the whole tool be driven in process by a test.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"

	"github.com/scottdensmore/rcptpixie/v2/version"
)

type ExitCode int

const (
	ExitOK          ExitCode = 0
	ExitFailure     ExitCode = 1
	ExitUsage       ExitCode = 2
	ExitPartial     ExitCode = 3
	ExitInterrupted ExitCode = 130
)

const (
	modeReceipts = "receipts"
	modeOrganize = "organize"
	modeUndo     = "undo"
)

// Env is everything Run touches outside its own arguments. Nothing in this
// package reads os.Args, calls os.Exit, or refers to flag.CommandLine.
type Env struct {
	Args   []string
	Stdout io.Writer // results and plan tables only
	Stderr io.Writer // logs, warnings, prompts, summary
	Stdin  io.Reader
	Getenv func(string) string
	IsTTY  func(any) bool
}

func Run(ctx context.Context, env Env) (code ExitCode) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(env.Stderr, "rcptpixie: internal error: %v\n\n%s\n", r, debug.Stack())
			code = ExitFailure
		}
	}()

	if ctx == nil {
		ctx = context.Background()
	}
	if env.Stdout == nil {
		env.Stdout = io.Discard
	}
	if env.Stderr == nil {
		env.Stderr = io.Discard
	}
	if env.Stdin == nil {
		env.Stdin = strings.NewReader("")
	}
	if env.Getenv == nil {
		env.Getenv = func(string) string { return "" }
	}
	if env.IsTTY == nil {
		env.IsTTY = func(any) bool { return false }
	}

	if len(env.Args) == 0 {
		rootUsage(env.Stderr)
		return ExitUsage
	}

	cmd, rest := route(env.Args)
	// A subcommand owns its own -h, so the pre-scan only covers the bare-path
	// form where there is no command flag set to hand it to.
	if !isCommand(env.Args[0]) {
		switch scanMeta(env.Args) {
		case "help":
			rootUsage(env.Stdout)
			return ExitOK
		case "version":
			fmt.Fprintln(env.Stdout, version.Get().String())
			return ExitOK
		}
	}

	switch cmd {
	case modeOrganize:
		code = runOrganize(ctx, env, rest)
	case modeUndo:
		code = runUndo(ctx, env, rest)
	default:
		code = runReceipts(ctx, env, rest)
	}
	if ctx.Err() != nil {
		return ExitInterrupted
	}
	return code
}

// route picks the subcommand. Anything that is not an exact command name,
// INCLUDING a leading flag, is receipts mode with the arguments untouched, which
// is what keeps `rcptpixie file.pdf` and `rcptpixie -verbose file.pdf` working.
func route(args []string) (cmd string, rest []string) {
	if len(args) == 0 {
		return "", nil
	}
	if isCommand(args[0]) {
		return args[0], args[1:]
	}
	return modeReceipts, args
}

func isCommand(s string) bool {
	switch s {
	case modeReceipts, modeOrganize, modeUndo:
		return true
	}
	return false
}

// scanMeta looks for a help or version request in the bare-path form, where
// there is no subcommand FlagSet to hand it to. It skips the operand of a value
// flag: a plain scan treats the "help" in `-model help dir/` as a request for
// usage and silently processes nothing, which is the failure this whole command
// layer exists to prevent.
func scanMeta(args []string) string {
	fs := newFlagSet("scan", io.Discard)
	(&opts{}).register(fs, nil)

	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			return ""
		}
		switch a {
		case "-h", "-help", "--help", "help":
			return "help"
		case "-version", "--version", "version":
			return "version"
		}
		if len(a) > 1 && a[0] == '-' && !strings.Contains(a, "=") && takesValue(fs, a) {
			i++
		}
	}
	return ""
}

// IsTerminal reports whether v is an interactive terminal, without depending on
// golang.org/x/term. It takes any so that stdin can be tested as well as stdout:
// a confirmation prompt is only answerable if the stream it READS from is a
// terminal.
func IsTerminal(v any) bool {
	f, ok := v.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func rootUsage(w io.Writer) {
	fmt.Fprint(w, `rcptpixie renames receipts and documents from what is written inside them.

Usage:
  rcptpixie <command> [flags] <file-or-directory>

Commands:
  receipts   rename receipts to "MM-DD-YYYY - TOTAL - Vendor - Category.ext" (default)
  organize   rename any document to "YYYY-MM-DD - Descriptive Subject.ext"
  undo       revert the renames recorded in a directory
  version    print version information
  help       print this message

Flags go after the subcommand: rcptpixie receipts -v ./dir
A bare path runs receipts mode: rcptpixie receipt.pdf
Use -- or ./name for a path that collides with a command name.

Common flags (see "rcptpixie <command> -h" for the full list):
  -model NAME   ollama model (default gemma4:e2b, env RCPTPIXIE_MODEL)
  -host URL     ollama base URL (env RCPTPIXIE_HOST, OLLAMA_HOST)
  -r            descend into subdirectories (off by default)
  -n            print the plan and rename nothing
  -y            skip the confirmation prompt
  -v, -q        more or less logging on stderr

Exit codes:
  0    everything succeeded
  1    nothing could be processed, or ollama is unreachable
  2    usage error, unknown path, or confirmation needed without a terminal
  3    some files were processed and some failed
  130  interrupted

Examples:
  rcptpixie receipt.pdf
  rcptpixie receipts -r ~/Receipts
  rcptpixie organize --dry-run ~/Downloads
  rcptpixie organize ~/Documents/Scans
  rcptpixie undo ~/Documents/Scans
`)
}
