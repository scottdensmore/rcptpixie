package cli

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"strings"

	"github.com/scottdensmore/rcptpixie/v2/internal/rename"
)

var ErrNotATTY = errors.New("confirmation required but stdin is not a terminal; pass -y to proceed")

func runUndo(ctx context.Context, env Env, args []string) ExitCode {
	if ctx.Err() != nil {
		return ExitInterrupted
	}

	var o opts
	flags := newFlagSet(modeUndo, env.Stderr)
	flags.BoolVar(&o.DryRun, "dry-run", false, "print what would be reverted and change nothing")
	flags.BoolVar(&o.DryRun, "n", false, "short for -dry-run")
	flags.BoolVar(&o.Yes, "yes", false, "answer yes to the confirmation prompt")
	flags.BoolVar(&o.Yes, "y", false, "short for -yes")
	flags.BoolVar(&o.Verbose, "verbose", false, "log debug detail to stderr")
	flags.BoolVar(&o.Verbose, "v", false, "short for -verbose")
	flags.BoolVar(&o.Quiet, "quiet", false, "log errors only")
	flags.BoolVar(&o.Quiet, "q", false, "short for -quiet")

	rest, err := o.parseInto(flags, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			commandUsage(env.Stdout, modeUndo, flags)
			return ExitOK
		}
		fmt.Fprintf(env.Stderr, "rcptpixie undo: %v\n\n", err)
		commandUsage(env.Stderr, modeUndo, flags)
		return ExitUsage
	}

	dir := "."
	switch len(rest) {
	case 0:
	case 1:
		dir = rest[0]
	default:
		fmt.Fprintf(env.Stderr, "rcptpixie undo: expected at most one directory, got %d\n\n", len(rest))
		commandUsage(env.Stderr, modeUndo, flags)
		return ExitUsage
	}

	log := newLogger(env.Stderr, levelFor(o.Verbose, o.Quiet))

	entries, err := rename.Read(dir, log)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			fmt.Fprintf(env.Stderr, "rcptpixie undo: no rename history in %s\n", dir)
			return ExitUsage
		}
		fmt.Fprintf(env.Stderr, "rcptpixie undo: %v\n", err)
		return ExitFailure
	}
	if len(entries) == 0 {
		fmt.Fprintf(env.Stderr, "rcptpixie undo: no rename history in %s\n", dir)
		return ExitUsage
	}

	renderUndo(env.Stdout, dir, entries)
	if o.DryRun {
		return ExitOK
	}

	if !o.Yes {
		ok, err := confirm(env.Stdin, env.Stderr, env.IsTTY(env.Stdin), fmt.Sprintf("Undo %d renames? [y/N] ", len(entries)))
		if err != nil {
			if errors.Is(err, ErrNotATTY) {
				fmt.Fprintln(env.Stderr, "rcptpixie undo: refusing to rename non-interactively without -y")
				return ExitUsage
			}
			fmt.Fprintf(env.Stderr, "rcptpixie undo: %v\n", err)
			return ExitUsage
		}
		if !ok {
			fmt.Fprintln(env.Stderr, "nothing was reverted")
			return ExitOK
		}
	}

	res, err := rename.Undo(dir, false, log)
	if err != nil {
		fmt.Fprintf(env.Stderr, "rcptpixie undo: %v\n", err)
		return ExitFailure
	}
	for _, d := range res.Details {
		fmt.Fprintf(env.Stderr, "  %s\n", d)
	}
	fmt.Fprintf(env.Stderr, "\n%d reverted, %d skipped\n", res.Reverted, res.Skipped)

	switch {
	case res.Skipped == 0:
		return ExitOK
	case res.Reverted > 0:
		return ExitPartial
	}
	return ExitFailure
}

// renderUndo prints the journal backwards, which is the order Undo reverts in.
func renderUndo(w io.Writer, dir string, entries []rename.Entry) {
	fmt.Fprintf(w, "UNDO — %d renames in %s\n\n", len(entries), dir)
	width := 0
	for _, e := range entries {
		if n := len([]rune(e.New)); n > width {
			width = n
		}
	}
	for i := len(entries) - 1; i >= 0; i-- {
		fmt.Fprintf(w, "  %s  ->  %s\n", padRunes(entries[i].New, width), entries[i].Old)
	}
}

func padRunes(s string, width int) string {
	if n := len([]rune(s)); n < width {
		return s + strings.Repeat(" ", width-n)
	}
	return s
}

// confirm never blocks on a hidden prompt: without a terminal it fails
// immediately, because a tool that waits for input inside CI hangs the job until
// the timeout with nothing on screen explaining why.
func confirm(in io.Reader, out io.Writer, isTTY bool, question string) (bool, error) {
	if !isTTY {
		return false, ErrNotATTY
	}
	fmt.Fprint(out, question)
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && line == "" {
		if errors.Is(err, io.EOF) {
			return false, nil
		}
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	}
	return false, nil
}
