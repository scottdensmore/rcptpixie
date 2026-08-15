package rename

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

const JournalName = ".rcptpixie-undo.jsonl"

type Entry struct {
	Time time.Time `json:"t"`
	Old  string    `json:"old"`
	New  string    `json:"new"`
}

// Journal is an append-only record of completed renames. A nil *Journal is a
// valid no-op receiver, so dry-run and un-journaled callers need no branches.
type Journal struct {
	f    *os.File
	path string
	log  *slog.Logger
}

// Open takes the logger so a warning about the journal reaches the command's
// own stderr at the level the user asked for, rather than the default slog
// output, which ignores -q and -v entirely.
func Open(dir string, log *slog.Logger) (*Journal, error) {
	path := filepath.Join(dir, JournalName)
	if err := dropTornTail(path); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	return &Journal{f: f, path: path, log: orDiscard(log)}, nil
}

// dropTornTail truncates a final line that never got its newline, which is what
// a write killed by a crash leaves behind. Read tolerates such a tail only while
// it is last: O_APPEND would splice the next entry onto it, turning a harmless
// remnant into a corrupt record with valid history stranded behind it.
func dropTornTail(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	if len(data) == 0 || data[len(data)-1] == '\n' {
		return nil
	}
	return os.Truncate(path, int64(bytes.LastIndexByte(data, '\n')+1))
}

// Append writes one entry and fsyncs it. The per-entry sync is deliberate: the
// run interrupted by Ctrl-C is the run whose undo history matters most.
func (j *Journal) Append(e Entry) error {
	if j == nil || j.f == nil {
		return nil
	}
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	if _, err := j.f.Write(line); err != nil {
		return err
	}
	return j.f.Sync()
}

func (j *Journal) Close() error {
	if j == nil || j.f == nil {
		return nil
	}
	err := j.f.Close()
	j.f = nil
	return err
}

func (j *Journal) Path() string {
	if j == nil {
		return ""
	}
	return j.path
}

// Read parses dir's journal. A truncated final line is skipped silently: it is
// the expected shape of a run killed mid-write. An unreadable line anywhere else
// is real damage, but it is only ever one record: skipping it with a warning
// keeps every valid rename around it revertable.
func Read(dir string, log *slog.Logger) ([]Entry, error) {
	log = orDiscard(log)
	path := filepath.Join(dir, JournalName)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lines := bytes.Split(data, []byte{'\n'})
	var entries []Entry
	for i, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			if i != len(lines)-1 {
				log.Warn("skipping unreadable undo journal line", "journal", path, "line", i+1, "err", err)
			}
			continue
		}
		entries = append(entries, e)
	}
	return entries, nil
}

type UndoResult struct {
	Reverted, Skipped int
	Details           []string
}

// Undo reverts entries in reverse order. Reverse matters: A->B then B->C must
// unwind backwards or the first revert recreates a collision.
func Undo(dir string, dryRun bool, log *slog.Logger) (UndoResult, error) {
	var res UndoResult
	entries, err := Read(dir, log)
	if err != nil {
		return res, err
	}
	reverted := make([]bool, len(entries))
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if !IsSafeBase(e.New) || !IsSafeBase(e.Old) {
			res.Skipped++
			res.Details = append(res.Details, fmt.Sprintf("%q: journal entry is not a plain file name", e.New))
			continue
		}
		newPath := filepath.Join(dir, e.New)
		fi, err := os.Lstat(newPath)
		if err != nil {
			res.Skipped++
			res.Details = append(res.Details, fmt.Sprintf("%q: no longer present", e.New))
			continue
		}
		if !fi.Mode().IsRegular() {
			res.Skipped++
			res.Details = append(res.Details, fmt.Sprintf("%q: not a regular file", e.New))
			continue
		}
		if _, err := os.Lstat(filepath.Join(dir, e.Old)); err == nil {
			res.Skipped++
			res.Details = append(res.Details, fmt.Sprintf("%q: original name %q is taken", e.New, e.Old))
			continue
		}
		if dryRun {
			res.Reverted++
			continue
		}
		it := Item{OldPath: newPath, NewName: e.Old, Action: ActionRename}
		if _, err := Apply(context.Background(), dir, it, nil); err != nil {
			res.Skipped++
			res.Details = append(res.Details, fmt.Sprintf("%q: %v", e.New, err))
			continue
		}
		res.Reverted++
		reverted[i] = true
	}
	if dryRun {
		return res, nil
	}
	var keep []Entry
	for i, e := range entries {
		if !reverted[i] {
			keep = append(keep, e)
		}
	}
	if err := settle(dir, keep); err != nil {
		return res, err
	}
	return res, nil
}

// settle replaces the journal with the entries undo did not revert, and removes
// it only once nothing is left. Dropping the whole file after a partial undo
// would discard exactly the renames undo had just refused to touch, making them
// unrevertable for good.
func settle(dir string, keep []Entry) error {
	path := filepath.Join(dir, JournalName)
	if len(keep) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	var buf bytes.Buffer
	for _, e := range keep {
		line, err := json.Marshal(e)
		if err != nil {
			return err
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	tmp, err := os.CreateTemp(dir, JournalName+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// orDiscard keeps a nil logger legal for callers that do not want output.
func orDiscard(log *slog.Logger) *slog.Logger {
	if log != nil {
		return log
	}
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// logger is nil-receiver safe, matching the rest of the Journal API.
func (j *Journal) logger() *slog.Logger {
	if j == nil || j.log == nil {
		return orDiscard(nil)
	}
	return j.log
}
