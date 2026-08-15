package cli

import (
	"errors"
	"flag"
	"io"
	"strings"
	"time"

	"github.com/scottdensmore/rcptpixie/internal/ollama"
)

// defaultReceiptExts is receipts mode's filter; organize mode defaults to the
// empty string, which means every regular file.
const defaultReceiptExts = ".pdf,.jpg,.jpeg,.png,.heic"

// defaultTimeout is per request, not per run: a cold multi-gigabyte model load
// on CPU is legitimately minutes.
const defaultTimeout = 5 * time.Minute

type opts struct {
	Model, Host                            string
	Timeout                                time.Duration
	Recursive, DryRun, Yes, Verbose, Quiet bool
	Exts                                   string
}

// newFlagSet always uses ContinueOnError. ExitOnError makes Parse call
// os.Exit(2) itself, which is why the shipped error branch was unreachable and
// why a bad flag in a table test would kill the test binary. The stdlib's own
// output is discarded because the caller prints one message of its own; out is
// where that caller writes.
func newFlagSet(name string, out io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	return fs
}

// register defines the flags, applying environment fallbacks to the defaults so
// that an explicit flag always wins over an environment variable.
func (o *opts) register(fs *flag.FlagSet, getenv func(string) string) {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	model := firstNonEmpty(getenv("RCPTPIXIE_MODEL"), ollama.DefaultModel)
	host := firstNonEmpty(getenv("RCPTPIXIE_HOST"), getenv("OLLAMA_HOST"), ollama.DefaultHost)

	fs.StringVar(&o.Model, "model", model, "ollama model to use (env RCPTPIXIE_MODEL)")
	fs.StringVar(&o.Host, "host", host, "ollama base URL (env RCPTPIXIE_HOST, OLLAMA_HOST)")
	fs.DurationVar(&o.Timeout, "timeout", defaultTimeout, "timeout for a single ollama request")
	fs.StringVar(&o.Exts, "ext", o.Exts, "comma-separated extensions to consider (empty means every file)")

	// The stdlib flag package has no aliases, so each short form is a second
	// binding onto the same target.
	fs.BoolVar(&o.Recursive, "recursive", false, "descend into subdirectories")
	fs.BoolVar(&o.Recursive, "r", false, "short for -recursive")
	fs.BoolVar(&o.DryRun, "dry-run", false, "print the plan and rename nothing")
	fs.BoolVar(&o.DryRun, "n", false, "short for -dry-run")
	fs.BoolVar(&o.Yes, "yes", false, "answer yes to the confirmation prompt")
	fs.BoolVar(&o.Yes, "y", false, "short for -yes")
	fs.BoolVar(&o.Verbose, "verbose", false, "log debug detail to stderr")
	fs.BoolVar(&o.Verbose, "v", false, "short for -verbose")
	fs.BoolVar(&o.Quiet, "quiet", false, "log errors only")
	fs.BoolVar(&o.Quiet, "q", false, "short for -quiet")
}

// validate checks the options that were actually registered. undo defines
// neither -host nor -timeout because it never contacts the server, so its
// zero-valued Host and Timeout must not be judged here.
func (o *opts) validate(fs *flag.FlagSet) error {
	if o.Verbose && o.Quiet {
		return errors.New("-verbose and -quiet cannot be used together")
	}
	if fs.Lookup("host") == nil {
		return nil
	}
	if o.Timeout <= 0 {
		return errors.New("-timeout must be greater than zero")
	}
	if _, err := ollama.New(o.Host, o.Timeout, nil); err != nil {
		return err
	}
	return nil
}

// parseInto parses args into o and returns the positionals FROM THE SAME
// FlagSet. Every subcommand goes through here, so flag permutation and
// validation cannot drift apart between them. The shipped bug was exactly this:
// parseFlags discarded fs.Args() and main read the global flag.Args(), which was
// never parsed and so was always empty.
func (o *opts) parseInto(fs *flag.FlagSet, args []string) ([]string, error) {
	if err := fs.Parse(permute(fs, args)); err != nil {
		return nil, err
	}
	if err := o.validate(fs); err != nil {
		return nil, err
	}
	return fs.Args(), nil
}

// permute moves flags ahead of positional arguments so that
// `rcptpixie receipt.pdf -n` works as well as `rcptpixie -n receipt.pdf`.
// The stdlib flag package stops parsing at the first non-flag, which would
// otherwise reject the trailing -n as a second path. Everything after a literal
// "--" is left positional, and an unknown flag is passed through untouched so
// Parse still reports it.
func permute(fs *flag.FlagSet, args []string) []string {
	flags := make([]string, 0, len(args))
	rest := make([]string, 0, len(args))

	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			rest = append(rest, args[i+1:]...)
			i = len(args)
		case len(a) > 1 && a[0] == '-':
			flags = append(flags, a)
			// A value flag written as "-model gemma" carries its value in the
			// next argument; "-model=gemma" and boolean flags do not.
			if !strings.Contains(a, "=") && takesValue(fs, a) && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
		default:
			rest = append(rest, a)
		}
	}
	if len(rest) == 0 {
		return flags
	}
	// The terminator has to survive into the output, otherwise a positional that
	// legitimately begins with "-" would be parsed as a flag on the way back in.
	return append(append(flags, "--"), rest...)
}

// takesValue reports whether the named flag consumes the following argument.
// Boolean flags expose IsBoolFlag; an unknown flag reports false so that its
// operand is preserved as a positional and Parse produces the real error.
func takesValue(fs *flag.FlagSet, arg string) bool {
	name := strings.TrimLeft(arg, "-")
	f := fs.Lookup(name)
	if f == nil {
		return false
	}
	b, ok := f.Value.(interface{ IsBoolFlag() bool })
	return !ok || !b.IsBoolFlag()
}

func splitExts(s string) []string {
	var exts []string
	for _, p := range strings.Split(s, ",") {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "" {
			continue
		}
		if !strings.HasPrefix(p, ".") {
			p = "." + p
		}
		exts = append(exts, p)
	}
	return exts
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}
