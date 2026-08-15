package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/scottdensmore/rcptpixie/v2/internal/analyze"
	"github.com/scottdensmore/rcptpixie/v2/internal/doc"
	"github.com/scottdensmore/rcptpixie/v2/internal/ollama"
	"github.com/scottdensmore/rcptpixie/v2/internal/rename"
)

func runReceipts(ctx context.Context, env Env, args []string) ExitCode {
	return execute(ctx, env, args, modeReceipts)
}

func runOrganize(ctx context.Context, env Env, args []string) ExitCode {
	return execute(ctx, env, args, modeOrganize)
}

func execute(ctx context.Context, env Env, args []string, mode string) ExitCode {
	o := &opts{}
	if mode == modeReceipts {
		o.Exts = defaultReceiptExts
	}
	flags := newFlagSet(mode, env.Stderr)
	o.register(flags, env.Getenv)

	rest, err := o.parseInto(flags, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			commandUsage(env.Stdout, mode, flags)
			return ExitOK
		}
		fmt.Fprintf(env.Stderr, "rcptpixie %s: %v\n\n", mode, err)
		commandUsage(env.Stderr, mode, flags)
		return ExitUsage
	}

	if len(rest) != 1 {
		fmt.Fprintf(env.Stderr, "rcptpixie %s: expected exactly one path, got %d\n\n", mode, len(rest))
		commandUsage(env.Stderr, mode, flags)
		return ExitUsage
	}
	root := rest[0]
	fi, err := os.Stat(root)
	if err != nil {
		fmt.Fprintf(env.Stderr, "rcptpixie %s: cannot use %s: %v\n", mode, root, err)
		return ExitUsage
	}
	// The walk drops fifos, devices and sockets in candidate(); an explicitly
	// named one has to be refused here or reading it blocks forever, with no
	// timeout covering the open.
	if !fi.IsDir() && !fi.Mode().IsRegular() {
		fmt.Fprintf(env.Stderr, "rcptpixie %s: cannot use %s: not a regular file\n", mode, root)
		return ExitUsage
	}

	log := newLogger(env.Stderr, levelFor(o.Verbose, o.Quiet))

	client, err := ollama.New(o.Host, o.Timeout, log)
	if err != nil {
		fmt.Fprintf(env.Stderr, "rcptpixie: %v\n", err)
		return ExitFailure
	}
	// Once, before any file work: N confusing per-file dial errors become one
	// actionable message in milliseconds.
	if err := client.Preflight(ctx, o.Model); err != nil {
		fmt.Fprintf(env.Stderr, "rcptpixie: %v\n", err)
		return ExitFailure
	}

	raster := doc.Detect(log)
	an := &analyze.Analyzer{C: client, Model: o.Model, Log: log}

	var (
		files       []string
		skippedDirs int
	)
	if fi.IsDir() {
		files, skippedDirs, err = collectLog(root, o.Recursive, splitExts(o.Exts), log)
		if err != nil {
			fmt.Fprintf(env.Stderr, "rcptpixie: cannot read %s: %v\n", root, err)
			return ExitFailure
		}
		if skippedDirs > 0 {
			log.Warn(fmt.Sprintf("%d subdirectories skipped; pass -r to include them", skippedDirs))
		}
	} else {
		files = []string{root}
	}
	if len(files) == 0 {
		fmt.Fprintf(env.Stderr, "rcptpixie %s: no matching files in %s\n", mode, root)
		return ExitOK
	}

	if !o.Quiet {
		fmt.Fprintf(env.Stderr, "Reading %d file(s) with %s; the first document may take a minute while the model loads.\n",
			len(files), o.Model)
	}

	// Sequential on purpose: a single ollama serializes generation per model, so
	// concurrency would buy a fraction of one model call at the price of an
	// ordered committer.
	items := make([]rename.Item, 0, len(files))
	for _, path := range files {
		if ctx.Err() != nil {
			break
		}
		items = append(items, buildItem(ctx, an, raster, mode, path, log))
	}

	plans := groupByDir(items)
	for _, p := range plans {
		if err := p.Resolve(); err != nil {
			fmt.Fprintf(env.Stderr, "rcptpixie: cannot plan renames in %s: %v\n", p.Dir, err)
			return ExitFailure
		}
	}

	for i, p := range plans {
		if i > 0 {
			fmt.Fprintln(env.Stdout)
		}
		p.Render(env.Stdout)
	}
	toRename, unchanged, skipped, failed := totals(plans)
	fmt.Fprintf(env.Stderr, "\n%d to rename, %d unchanged, %d skipped, %d failed\n", toRename, unchanged, skipped, failed)

	if o.DryRun {
		// A dry run reports the same health as a real one. Returning ExitOK
		// unconditionally would let `rcptpixie -n dir && rcptpixie dir` proceed
		// after a run in which every single file failed to be read.
		switch {
		case ctx.Err() != nil:
			return ExitInterrupted
		case failed == 0:
			return ExitOK
		case toRename+unchanged+skipped > 0:
			return ExitPartial
		}
		return ExitFailure
	}

	if mode == modeOrganize && !o.Yes && toRename > 0 {
		ok, err := confirm(env.Stdin, env.Stderr, env.IsTTY(env.Stdin), fmt.Sprintf("Rename %d files? [y/N] ", toRename))
		if err != nil {
			if errors.Is(err, ErrNotATTY) {
				fmt.Fprintln(env.Stderr, "rcptpixie: refusing to rename non-interactively without -y")
				return ExitUsage
			}
			fmt.Fprintf(env.Stderr, "rcptpixie: %v\n", err)
			return ExitUsage
		}
		if !ok {
			fmt.Fprintln(env.Stderr, "nothing was renamed")
			return ExitOK
		}
	}

	renamed := 0
	for _, p := range plans {
		renamed += applyPlan(ctx, env, p, log)
	}

	_, unchanged, skipped, failed = totals(plans)
	fmt.Fprintf(env.Stderr, "\n%d renamed, %d unchanged, %d skipped, %d failed\n", renamed, unchanged, skipped, failed)
	if renamed > 0 {
		for _, p := range plans {
			if hasRenames(p) {
				fmt.Fprintf(env.Stderr, "Undo with:  rcptpixie undo %s\n", p.Dir)
			}
		}
	}

	switch {
	case ctx.Err() != nil:
		return ExitInterrupted
	case failed == 0:
		return ExitOK
	case renamed+unchanged+skipped > 0:
		return ExitPartial
	}
	return ExitFailure
}

func applyPlan(ctx context.Context, env Env, p *rename.Plan, log *slog.Logger) int {
	if !hasRenames(p) {
		return 0
	}
	// The journal is opened on the first rename actually attempted, not up front,
	// and dropped again if nothing was ever appended: a run that is cancelled or
	// fails outright must not leave a stray dotfile in the user's directory.
	var (
		j      *rename.Journal
		opened bool
	)
	openJournal := func() *rename.Journal {
		if !opened {
			opened = true
			var err error
			if j, err = rename.Open(p.Dir, log); err != nil {
				log.Warn("could not open the undo journal; renames will not be reversible", "dir", p.Dir, "err", err)
			}
		}
		return j
	}
	defer func() {
		j.Close()
		dropEmptyJournal(j.Path())
	}()

	renamed := 0
	for i := range p.Items {
		it := &p.Items[i]
		if it.Action != rename.ActionRename {
			continue
		}
		if ctx.Err() != nil {
			return renamed
		}
		newPath, err := rename.Apply(ctx, p.Dir, *it, openJournal())
		if err != nil {
			it.Action = rename.ActionError
			it.Err = err
			log.Warn("rename failed", "file", it.OldPath, "err", err)
			continue
		}
		fmt.Fprintf(env.Stdout, "Renamed: %s -> %s\n", it.OldPath, newPath)
		renamed++
	}
	return renamed
}

// dropEmptyJournal removes a journal that never received an entry. Size, not the
// rename count, is what decides: a journal carrying an earlier run's history is
// non-empty and must survive a run that renamed nothing.
func dropEmptyJournal(path string) {
	if path == "" {
		return
	}
	if fi, err := os.Stat(path); err == nil && fi.Size() == 0 {
		os.Remove(path)
	}
}

func buildItem(ctx context.Context, an *analyze.Analyzer, r doc.Rasterizer, mode, path string, log *slog.Logger) rename.Item {
	base := filepath.Base(path)
	// Before any model call, so a second run over 500 files costs one directory
	// listing rather than 500 inferences.
	if mode == modeOrganize && analyze.IsOrganized(base) {
		return rename.Item{OldPath: path, Action: rename.ActionSkip, Reason: "already organized"}
	}

	d, err := doc.Load(ctx, path, r, log)
	if err != nil {
		return rename.Item{OldPath: path, Action: rename.ActionError, Err: err}
	}
	ext := filepath.Ext(path)

	if mode == modeOrganize {
		s, err := an.Subject(ctx, d)
		if err != nil {
			return rename.Item{OldPath: path, Action: rename.ActionError, Err: err}
		}
		if s.Date.IsZero() {
			s.Date = d.ModTime
			log.Debug("no date in the document, using its modification time", "file", path)
		}
		return rename.Item{OldPath: path, NewName: analyze.SubjectName(s, ext), Action: rename.ActionRename}
	}

	rc, err := an.Receipt(ctx, d)
	if err != nil {
		return rename.Item{OldPath: path, Action: rename.ActionError, Err: err}
	}
	return rename.Item{OldPath: path, NewName: analyze.ReceiptName(rc, ext), Action: rename.ActionRename}
}

// groupByDir splits the items into one plan per parent directory: collision
// resolution and the O_EXCL claim are both per directory, and a recursive run
// must never rename a file out of the subdirectory it was found in.
func groupByDir(items []rename.Item) []*rename.Plan {
	byDir := make(map[string]*rename.Plan)
	var dirs []string
	for _, it := range items {
		dir := filepath.Dir(it.OldPath)
		p, ok := byDir[dir]
		if !ok {
			p = &rename.Plan{Dir: dir}
			byDir[dir] = p
			dirs = append(dirs, dir)
		}
		p.Items = append(p.Items, it)
	}
	sort.Strings(dirs)
	plans := make([]*rename.Plan, 0, len(dirs))
	for _, d := range dirs {
		plans = append(plans, byDir[d])
	}
	return plans
}

func totals(plans []*rename.Plan) (toRename, unchanged, skipped, failed int) {
	for _, p := range plans {
		a, b, c, d := p.Tally()
		toRename += a
		unchanged += b
		skipped += c
		failed += d
	}
	return toRename, unchanged, skipped, failed
}

func hasRenames(p *rename.Plan) bool {
	for _, it := range p.Items {
		if it.Action == rename.ActionRename {
			return true
		}
	}
	return false
}

func collect(root string, recursive bool, exts []string) (paths []string, skippedDirs int, err error) {
	return collectLog(root, recursive, exts, nil)
}

// collectLog walks root. A per-entry error is logged and skipped, never
// returned: the shipped code returned it, so one unreadable subdirectory or a
// file removed mid-walk aborted every remaining file.
func collectLog(root string, recursive bool, exts []string, log *slog.Logger) (paths []string, skippedDirs int, err error) {
	if log == nil {
		log = newLogger(io.Discard, slog.LevelError)
	}

	if !recursive {
		ents, err := os.ReadDir(root)
		if err != nil {
			return nil, 0, err
		}
		for _, e := range ents {
			full := filepath.Join(root, e.Name())
			if strings.HasPrefix(e.Name(), ".") {
				continue
			}
			if e.IsDir() {
				if dirHasCandidates(full, exts) {
					skippedDirs++
				}
				continue
			}
			if candidate(full, exts) {
				paths = append(paths, full)
			}
		}
		sort.Strings(paths)
		return paths, skippedDirs, nil
	}

	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			log.Warn("skipping unreadable entry", "path", path, "err", err)
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if path != root && strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if candidate(path, exts) {
			paths = append(paths, path)
		}
		return nil
	})
	if walkErr != nil {
		return nil, 0, walkErr
	}
	sort.Strings(paths)
	return paths, 0, nil
}

// candidate rejects dotfiles, the journal, symlinks and other non-regular files,
// and empty files, all of which would otherwise reach the model.
func candidate(path string, exts []string) bool {
	base := filepath.Base(path)
	if strings.HasPrefix(base, ".") || base == rename.JournalName {
		return false
	}
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() == 0 {
		return false
	}
	return doc.IsSupported(path, exts)
}

func dirHasCandidates(dir string, exts []string) bool {
	found := false
	filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != dir && strings.HasPrefix(d.Name(), ".") {
				return fs.SkipDir
			}
			return nil
		}
		if candidate(path, exts) {
			found = true
			return fs.SkipAll
		}
		return nil
	})
	return found
}

var modeBlurb = map[string]string{
	modeReceipts: `Renames receipts to "MM-DD-YYYY - TOTAL - Vendor - Category.ext".`,
	modeOrganize: `Renames documents to "YYYY-MM-DD - Descriptive Subject.ext", asking before it does.`,
	modeUndo:     "Reverts the renames recorded in a directory's .rcptpixie-undo.jsonl.",
}

func commandUsage(w io.Writer, mode string, flags *flag.FlagSet) {
	arg := "<file-or-directory>"
	if mode == modeUndo {
		arg = "[directory]"
	}
	fmt.Fprintf(w, "Usage: rcptpixie %s [flags] %s\n\n%s\n\nFlags:\n", mode, arg, modeBlurb[mode])
	flags.SetOutput(w)
	flags.PrintDefaults()
	flags.SetOutput(io.Discard)
	if mode != modeUndo {
		fmt.Fprintf(w, "\nSubdirectories are skipped unless -r is given.\n")
	}
}
