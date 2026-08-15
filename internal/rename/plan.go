package rename

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

type Action string

const (
	ActionRename    Action = "rename"
	ActionUnchanged Action = "unchanged"
	ActionSkip      Action = "skip"
	ActionError     Action = "error"
)

type Item struct {
	OldPath string
	NewName string // base name only, already sanitized; "" unless ActionRename
	Action  Action
	Reason  string // for ActionSkip
	Err     error  // for ActionError
}

type Plan struct {
	Dir   string
	Items []Item
}

var ErrTooManyCollisions = errors.New("more than 999 name collisions")

// Resolve assigns " (2)", " (3)" ... suffixes to any name that collides with
// another plan item or with a file already on disk, and downgrades no-op
// renames to ActionUnchanged. It must run before rendering or applying.
func (p *Plan) Resolve() error {
	ents, err := os.ReadDir(p.Dir)
	if err != nil {
		return err
	}
	// Keyed case-insensitively so a case-insensitive filesystem cannot be
	// tricked into an overwrite. Counted rather than flagged, because two files
	// on a case-SENSITIVE filesystem can share one folded key ("A.pdf" and
	// "a.pdf"); discounting a file's own entry must not also discount its
	// neighbour's.
	onDisk := make(map[string]int, len(ents))
	for _, e := range ents {
		onDisk[strings.ToLower(e.Name())]++
	}
	// Names promised to an earlier item in this same batch. Kept apart from the
	// on-disk counts because a reservation is never discounted for anyone.
	claimed := make(map[string]bool, len(p.Items))

	for i := range p.Items {
		it := &p.Items[i]
		if it.Action != ActionRename {
			continue
		}
		oldBase := filepath.Base(it.OldPath)
		if it.NewName == oldBase {
			it.Action = ActionUnchanged
			continue
		}
		// A file's own name is not a collision with itself, so drop exactly one
		// entry — its own — from the count for the duration of the search.
		ownKey := strings.ToLower(oldBase)
		onDisk[ownKey]--

		name, ok := freeName(onDisk, claimed, it.NewName)
		switch {
		case !ok:
			it.Action = ActionError
			it.Err = ErrTooManyCollisions
			it.NewName = ""
		case name == oldBase:
			// The suffix search landed back on the file's own name: a second run
			// over an already-suffixed "X (2).pdf" is not a rename, and reporting
			// it as one misstates what the run will do. No claim is needed — the
			// file's own on-disk entry already reserves the name.
			it.Action = ActionUnchanged
			it.NewName = name
		default:
			it.NewName = name
			claimed[strings.ToLower(name)] = true
		}
		onDisk[ownKey]++
	}
	return nil
}

// freeName returns the first of name, "name (2)", "name (3)" ... that is not
// already in taken.
func freeName(onDisk map[string]int, claimed map[string]bool, name string) (string, bool) {
	free := func(candidate string) bool {
		k := strings.ToLower(candidate)
		return onDisk[k] <= 0 && !claimed[k]
	}
	if free(name) {
		return name, true
	}
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for n := 2; n <= 999; n++ {
		cand := suffixed(stem, ext, n)
		if free(cand) {
			return cand, true
		}
	}
	return "", false
}

func suffixed(stem, ext string, n int) string {
	suffix := fmt.Sprintf(" (%d)", n)
	if len(stem)+len(suffix)+len(ext) <= 255 {
		return stem + suffix + ext
	}
	limit := maxBaseLen - len(suffix) - len(ext)
	if limit < 0 {
		limit = 0
	}
	if limit > len(stem) {
		limit = len(stem)
	}
	for limit > 0 && !utf8.RuneStart(stem[limit]) {
		limit--
	}
	return SanitizeFilename(strings.TrimRight(stem[:limit], trimCutset)+suffix, ext)
}

func (p *Plan) Tally() (renamed, unchanged, skipped, failed int) {
	for _, it := range p.Items {
		switch it.Action {
		case ActionRename:
			renamed++
		case ActionUnchanged:
			unchanged++
		case ActionSkip:
			skipped++
		case ActionError:
			failed++
		}
	}
	return renamed, unchanged, skipped, failed
}

// Render writes the before/after table used by both -dry-run and the organize
// confirmation prompt, so what the user confirms is exactly what -dry-run
// showed. The caller prints the trailing counts.
func (p *Plan) Render(w io.Writer) {
	noun := "files"
	if len(p.Items) == 1 {
		noun = "file"
	}
	fmt.Fprintf(w, "PLAN — %d %s in %s\n\n", len(p.Items), noun, p.Dir)

	var wOld, wNew int
	for _, it := range p.Items {
		if n := utf8.RuneCountInString(filepath.Base(it.OldPath)); n > wOld {
			wOld = n
		}
		if it.Action == ActionRename {
			if n := utf8.RuneCountInString(it.NewName); n > wNew {
				wNew = n
			}
		}
	}

	for _, it := range p.Items {
		old := filepath.Base(it.OldPath)
		if it.Action == ActionRename {
			fmt.Fprintf(w, "  %s  ->  %s  rename\n", pad(old, wOld), pad(it.NewName, wNew))
			continue
		}
		fmt.Fprintf(w, "  %s  --  %s\n", pad(old, wOld), note(it))
	}
}

func note(it Item) string {
	switch it.Action {
	case ActionUnchanged:
		return "unchanged"
	case ActionSkip:
		if it.Reason != "" {
			return "skipped: " + it.Reason
		}
		return "skipped"
	case ActionError:
		if it.Err != nil {
			return "error: " + it.Err.Error()
		}
		return "error"
	}
	return string(it.Action)
}

// pad right-pads s to width display columns, counting runes rather than bytes
// so accented vendor names stay in line.
func pad(s string, width int) string {
	if n := utf8.RuneCountInString(s); n < width {
		return s + strings.Repeat(" ", width-n)
	}
	return s
}
