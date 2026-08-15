package rename_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/scottdensmore/rcptpixie/internal/rename"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// listing returns the sorted names of dir's entries.
func listing(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}
	names := make([]string, 0, len(ents))
	for _, e := range ents {
		names = append(names, e.Name())
	}
	slices.Sort(names)
	return names
}

// walkRel returns every path under root, relative to root, sorted.
func walkRel(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	slices.Sort(out)
	return out
}

// TestApplyNeverOverwrites is the data-loss regression test: os.Rename would
// have destroyed the destination silently.
func TestApplyNeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "scan_0001.pdf")
	dst := filepath.Join(dir, "2023-01-15 - Whole Foods.pdf")
	writeFile(t, src, "source bytes")
	writeFile(t, dst, "PRECIOUS ORIGINAL")

	it := rename.Item{OldPath: src, NewName: "2023-01-15 - Whole Foods.pdf", Action: rename.ActionRename}
	newPath, err := rename.Apply(context.Background(), dir, it, nil)
	if !errors.Is(err, rename.ErrTargetExists) {
		t.Fatalf("Apply err = %v, want ErrTargetExists", err)
	}
	if newPath != "" {
		t.Errorf("Apply newPath = %q, want empty on failure", newPath)
	}
	if got := readFile(t, dst); got != "PRECIOUS ORIGINAL" {
		t.Errorf("destination was modified: %q", got)
	}
	if got := readFile(t, src); got != "source bytes" {
		t.Errorf("source was modified: %q", got)
	}
	want := []string{"2023-01-15 - Whole Foods.pdf", "scan_0001.pdf"}
	if got := listing(t, dir); !slices.Equal(got, want) {
		t.Errorf("listing = %v, want %v", got, want)
	}
}

func TestApplyRemovesPlaceholderOnFailure(t *testing.T) {
	dir := t.TempDir()
	// A source that does not exist makes os.Rename fail after the O_EXCL claim
	// has already created the target placeholder.
	it := rename.Item{
		OldPath: filepath.Join(dir, "vanished.pdf"),
		NewName: "2023-01-15 - Whole Foods.pdf",
		Action:  rename.ActionRename,
	}
	if _, err := rename.Apply(context.Background(), dir, it, nil); err == nil {
		t.Fatal("Apply on a missing source succeeded, want error")
	}
	if got := listing(t, dir); len(got) != 0 {
		t.Errorf("placeholder left behind: %v", got)
	}
}

// TestApplyDoesNotJournalFailedRename is the phantom-entry regression test: a
// record of a rename that never happened sends a later undo at whoever holds
// that name next.
func TestApplyDoesNotJournalFailedRename(t *testing.T) {
	dir := t.TempDir()
	j, err := rename.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	it := rename.Item{
		OldPath: filepath.Join(dir, "vanished.pdf"),
		NewName: "2023-01-15 - Whole Foods.pdf",
		Action:  rename.ActionRename,
	}
	if _, err := rename.Apply(context.Background(), dir, it, j); err == nil {
		t.Fatal("Apply on a missing source succeeded, want error")
	}
	if err := j.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	entries, err := rename.Read(dir, nil)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("failed rename left %d journal entries: %+v", len(entries), entries)
	}

	// An unrelated file lands on the name the failed rename would have produced.
	victim := filepath.Join(dir, "2023-01-15 - Whole Foods.pdf")
	writeFile(t, victim, "MY IMPORTANT TAX FILE")
	res, err := rename.Undo(dir, false, nil)
	if err != nil {
		t.Fatalf("Undo: %v", err)
	}
	if res.Reverted != 0 {
		t.Errorf("Undo = %+v, want 0 reverted", res)
	}
	if got := readFile(t, victim); got != "MY IMPORTANT TAX FILE" {
		t.Errorf("undo moved an untouched file: %q", got)
	}
}

func TestApplyRejectsUnsafeName(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "work")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "src.pdf")
	writeFile(t, src, "keep me")

	for _, name := range []string{"../escape.pdf", "a/b.pdf", "..", "", ".", `..\escape.pdf`} {
		it := rename.Item{OldPath: src, NewName: name, Action: rename.ActionRename}
		_, err := rename.Apply(context.Background(), dir, it, nil)
		if !errors.Is(err, rename.ErrUnsafeName) {
			t.Errorf("Apply(%q) err = %v, want ErrUnsafeName", name, err)
		}
	}

	want := []string{"work", "work/src.pdf"}
	if got := walkRel(t, root); !slices.Equal(got, want) {
		t.Errorf("tree = %v, want %v (something escaped the target directory)", got, want)
	}
	if got := readFile(t, src); got != "keep me" {
		t.Errorf("source was modified: %q", got)
	}
}

func TestApplyCaseOnlyRename(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "FOO.pdf")
	writeFile(t, src, "case only")

	it := rename.Item{OldPath: src, NewName: "Foo.pdf", Action: rename.ActionRename}
	newPath, err := rename.Apply(context.Background(), dir, it, nil)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := readFile(t, newPath); got != "case only" {
		t.Errorf("content = %q, want %q", got, "case only")
	}
	want := []string{"Foo.pdf"}
	if got := listing(t, dir); !slices.Equal(got, want) {
		t.Errorf("listing = %v, want %v", got, want)
	}
}

func TestApplySameNameIsNoOp(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "already-named.pdf")
	writeFile(t, src, "unchanged")

	it := rename.Item{OldPath: src, NewName: "already-named.pdf", Action: rename.ActionRename}
	newPath, err := rename.Apply(context.Background(), dir, it, nil)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if newPath != src {
		t.Errorf("newPath = %q, want %q", newPath, src)
	}
	if got := readFile(t, src); got != "unchanged" {
		t.Errorf("content = %q", got)
	}
}

func TestApplyCancelledContext(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a.pdf")
	writeFile(t, src, "a")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	it := rename.Item{OldPath: src, NewName: "b.pdf", Action: rename.ActionRename}
	if _, err := rename.Apply(ctx, dir, it, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("Apply err = %v, want context.Canceled", err)
	}
	want := []string{"a.pdf"}
	if got := listing(t, dir); !slices.Equal(got, want) {
		t.Errorf("listing = %v, want %v", got, want)
	}
}

func TestPlanResolveBatchCollision(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"scan1.pdf", "scan2.pdf", "scan3.pdf"} {
		writeFile(t, filepath.Join(dir, n), n)
	}
	p := &rename.Plan{Dir: dir, Items: []rename.Item{
		{OldPath: filepath.Join(dir, "scan1.pdf"), NewName: "Whole Foods.pdf", Action: rename.ActionRename},
		{OldPath: filepath.Join(dir, "scan2.pdf"), NewName: "Whole Foods.pdf", Action: rename.ActionRename},
		{OldPath: filepath.Join(dir, "scan3.pdf"), NewName: "Whole Foods.pdf", Action: rename.ActionRename},
	}}
	if err := p.Resolve(); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	want := []string{"Whole Foods.pdf", "Whole Foods (2).pdf", "Whole Foods (3).pdf"}
	for i, w := range want {
		if p.Items[i].NewName != w {
			t.Errorf("item %d NewName = %q, want %q", i, p.Items[i].NewName, w)
		}
		if p.Items[i].Action != rename.ActionRename {
			t.Errorf("item %d Action = %q, want rename", i, p.Items[i].Action)
		}
	}

	// The suffixes must survive contact with the filesystem, too.
	for _, it := range p.Items {
		if _, err := rename.Apply(context.Background(), dir, it, nil); err != nil {
			t.Fatalf("Apply %q: %v", it.NewName, err)
		}
	}
	got := listing(t, dir)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("listing = %v, want %v", got, want)
	}
}

func TestPlanResolveExistingCollision(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "scan1.pdf"), "scan")
	writeFile(t, filepath.Join(dir, "Whole Foods.pdf"), "ORIGINAL")

	p := &rename.Plan{Dir: dir, Items: []rename.Item{
		{OldPath: filepath.Join(dir, "scan1.pdf"), NewName: "Whole Foods.pdf", Action: rename.ActionRename},
	}}
	if err := p.Resolve(); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := p.Items[0].NewName; got != "Whole Foods (2).pdf" {
		t.Fatalf("NewName = %q, want %q", got, "Whole Foods (2).pdf")
	}
	if _, err := rename.Apply(context.Background(), dir, p.Items[0], nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := readFile(t, filepath.Join(dir, "Whole Foods.pdf")); got != "ORIGINAL" {
		t.Errorf("existing file was modified: %q", got)
	}
}

func TestPlanResolveCaseInsensitive(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "scan1.pdf"), "scan")
	writeFile(t, filepath.Join(dir, "chase statement.pdf"), "ORIGINAL")

	p := &rename.Plan{Dir: dir, Items: []rename.Item{
		{OldPath: filepath.Join(dir, "scan1.pdf"), NewName: "Chase Statement.pdf", Action: rename.ActionRename},
	}}
	if err := p.Resolve(); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := p.Items[0].NewName; got != "Chase Statement (2).pdf" {
		t.Fatalf("NewName = %q, want %q", got, "Chase Statement (2).pdf")
	}
	if got := readFile(t, filepath.Join(dir, "chase statement.pdf")); got != "ORIGINAL" {
		t.Errorf("existing file was modified: %q", got)
	}
}

func TestPlanResolveUnchanged(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "Whole Foods.pdf"), "x")
	p := &rename.Plan{Dir: dir, Items: []rename.Item{
		{OldPath: filepath.Join(dir, "Whole Foods.pdf"), NewName: "Whole Foods.pdf", Action: rename.ActionRename},
	}}
	if err := p.Resolve(); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if p.Items[0].Action != rename.ActionUnchanged {
		t.Errorf("Action = %q, want unchanged", p.Items[0].Action)
	}
	renamed, unchanged, skipped, failed := p.Tally()
	if renamed != 0 || unchanged != 1 || skipped != 0 || failed != 0 {
		t.Errorf("Tally = %d,%d,%d,%d, want 0,1,0,0", renamed, unchanged, skipped, failed)
	}
}

func TestJournalRoundTrip(t *testing.T) {
	dir := t.TempDir()
	j, err := rename.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !strings.HasSuffix(j.Path(), rename.JournalName) {
		t.Errorf("Path = %q, want a %s suffix", j.Path(), rename.JournalName)
	}
	want := []rename.Entry{
		{Time: time.Now().UTC(), Old: "a.pdf", New: "Alpha.pdf"},
		{Time: time.Now().UTC(), Old: "b.pdf", New: "Bravo.pdf"},
		{Time: time.Now().UTC(), Old: "Ünïcode ✓.pdf", New: "Charlie.pdf"},
	}
	for _, e := range want {
		if err := j.Append(e); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	if err := j.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	got, err := rename.Read(dir, nil)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("Read returned %d entries, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Old != want[i].Old || got[i].New != want[i].New {
			t.Errorf("entry %d = %+v, want %+v", i, got[i], want[i])
		}
		if !got[i].Time.Equal(want[i].Time) {
			t.Errorf("entry %d time = %v, want %v", i, got[i].Time, want[i].Time)
		}
	}

	// A nil journal is a valid no-op receiver.
	var nilJ *rename.Journal
	if err := nilJ.Append(rename.Entry{}); err != nil {
		t.Errorf("nil Append: %v", err)
	}
	if err := nilJ.Close(); err != nil {
		t.Errorf("nil Close: %v", err)
	}
	if p := nilJ.Path(); p != "" {
		t.Errorf("nil Path = %q, want empty", p)
	}
}

func TestUndoRestoresOriginals(t *testing.T) {
	dir := t.TempDir()
	originals := []string{"scan_0001.pdf", "scan_0002.pdf", "scan_0003.pdf", "scan_0004.pdf", "scan_0005.pdf"}
	for i, n := range originals {
		writeFile(t, filepath.Join(dir, n), string(rune('a'+i)))
	}
	before := listing(t, dir)

	j, err := rename.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	for i, n := range originals {
		it := rename.Item{
			OldPath: filepath.Join(dir, n),
			NewName: "2023-01-1" + string(rune('1'+i)) + " - Whole Foods.pdf",
			Action:  rename.ActionRename,
		}
		if _, err := rename.Apply(context.Background(), dir, it, j); err != nil {
			t.Fatalf("Apply %s: %v", n, err)
		}
	}
	if err := j.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	for _, n := range originals {
		if _, err := os.Stat(filepath.Join(dir, n)); err == nil {
			t.Fatalf("%s was not renamed", n)
		}
	}

	res, err := rename.Undo(dir, false, nil)
	if err != nil {
		t.Fatalf("Undo: %v", err)
	}
	if res.Reverted != len(originals) || res.Skipped != 0 {
		t.Errorf("Undo = %+v, want 5 reverted 0 skipped", res)
	}
	if got := listing(t, dir); !slices.Equal(got, before) {
		t.Errorf("listing = %v, want %v", got, before)
	}
	for i, n := range originals {
		if got := readFile(t, filepath.Join(dir, n)); got != string(rune('a'+i)) {
			t.Errorf("%s content = %q", n, got)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, rename.JournalName)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("journal still present after undo: %v", err)
	}
}

func TestUndoSkipsWhenOriginalTaken(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a.pdf")
	writeFile(t, src, "renamed content")

	j, err := rename.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	it := rename.Item{OldPath: src, NewName: "b.pdf", Action: rename.ActionRename}
	if _, err := rename.Apply(context.Background(), dir, it, j); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if err := j.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Something else now occupies the original name.
	writeFile(t, src, "NEW OCCUPANT")

	res, err := rename.Undo(dir, false, nil)
	if err != nil {
		t.Fatalf("Undo: %v", err)
	}
	if res.Reverted != 0 || res.Skipped != 1 {
		t.Errorf("Undo = %+v, want 0 reverted 1 skipped", res)
	}
	if len(res.Details) != 1 || !strings.Contains(res.Details[0], "a.pdf") {
		t.Errorf("Details = %v, want a reason naming a.pdf", res.Details)
	}
	if got := readFile(t, src); got != "NEW OCCUPANT" {
		t.Errorf("occupant was overwritten: %q", got)
	}
	if got := readFile(t, filepath.Join(dir, "b.pdf")); got != "renamed content" {
		t.Errorf("b.pdf content = %q", got)
	}
}

// TestUndoKeepsSkippedEntries: a rename undo refused to revert has to stay
// revertable once the obstacle is cleared.
func TestUndoKeepsSkippedEntries(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a.pdf")
	writeFile(t, src, "renamed content")

	other := filepath.Join(dir, "other.pdf")
	writeFile(t, other, "other content")

	j, err := rename.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	for _, it := range []rename.Item{
		{OldPath: src, NewName: "Alpha.pdf", Action: rename.ActionRename},
		{OldPath: other, NewName: "Bravo.pdf", Action: rename.ActionRename},
	} {
		if _, err := rename.Apply(context.Background(), dir, it, j); err != nil {
			t.Fatalf("Apply %q: %v", it.NewName, err)
		}
	}
	if err := j.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// A re-scan drops a new file under the original name, so Alpha.pdf cannot be
	// reverted while Bravo.pdf can.
	writeFile(t, src, "NEW OCCUPANT")

	res, err := rename.Undo(dir, false, nil)
	if err != nil {
		t.Fatalf("first Undo: %v", err)
	}
	if res.Reverted != 1 || res.Skipped != 1 {
		t.Fatalf("first Undo = %+v, want 1 reverted 1 skipped", res)
	}

	entries, err := rename.Read(dir, nil)
	if err != nil {
		t.Fatalf("Read after partial undo: %v", err)
	}
	if len(entries) != 1 || entries[0].New != "Alpha.pdf" || entries[0].Old != "a.pdf" {
		t.Fatalf("journal = %+v, want only the skipped a.pdf -> Alpha.pdf entry", entries)
	}

	// Clear the obstacle: the skipped rename must still be revertable.
	if err := os.Remove(src); err != nil {
		t.Fatal(err)
	}
	res, err = rename.Undo(dir, false, nil)
	if err != nil {
		t.Fatalf("second Undo: %v", err)
	}
	if res.Reverted != 1 || res.Skipped != 0 {
		t.Errorf("second Undo = %+v, want 1 reverted 0 skipped", res)
	}
	if got := listing(t, dir); !slices.Equal(got, []string{"a.pdf", "other.pdf"}) {
		t.Errorf("listing = %v, want [a.pdf other.pdf]", got)
	}
	if got := readFile(t, src); got != "renamed content" {
		t.Errorf("a.pdf content = %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, rename.JournalName)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("journal still present after a complete undo: %v", err)
	}
}

// TestJournalAppendAfterTornTail: an unterminated final line must not swallow
// the next record and corrupt the history behind it.
func TestJournalAppendAfterTornTail(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, rename.JournalName),
		`{"t":"2023-01-15T10:00:00Z","old":"a.pdf","new":"Alpha.pdf"}`+"\n"+
			`{"t":"2023-01-15T10:00:01Z","old":"b.pdf","ne`)

	j, err := rename.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := j.Append(rename.Entry{Time: time.Now().UTC(), Old: "c.pdf", New: "Charlie.pdf"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := j.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	entries, err := rename.Read(dir, nil)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("Read = %+v, want the surviving entry plus the appended one", entries)
	}
	if entries[0].New != "Alpha.pdf" || entries[1].New != "Charlie.pdf" {
		t.Errorf("entries = %+v, want Alpha.pdf then Charlie.pdf", entries)
	}
}

// TestUndoTolerantOfCorruptLine covers a journal an older run already poisoned:
// one unreadable record must not strand the valid ones around it.
func TestUndoTolerantOfCorruptLine(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "Alpha.pdf"), "alpha")
	writeFile(t, filepath.Join(dir, "Charlie.pdf"), "charlie")
	writeFile(t, filepath.Join(dir, rename.JournalName),
		`{"t":"2023-01-15T10:00:00Z","old":"a.pdf","new":"Alpha.pdf"}`+"\n"+
			`{"t":"2023-01-15T10:00:01Z","old":"b.pdf","ne{"t":"2023-01-15T10:00:02Z","old":"c.pdf","new":"Charlie.pdf"}`+"\n"+
			`{"t":"2023-01-15T10:00:03Z","old":"d.pdf","new":"Delta.pdf"}`+"\n")

	entries, err := rename.Read(dir, nil)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(entries) != 2 || entries[0].New != "Alpha.pdf" || entries[1].New != "Delta.pdf" {
		t.Fatalf("Read = %+v, want the two readable entries", entries)
	}

	res, err := rename.Undo(dir, false, nil)
	if err != nil {
		t.Fatalf("Undo: %v", err)
	}
	if res.Reverted != 1 || res.Skipped != 1 {
		t.Errorf("Undo = %+v, want 1 reverted (Alpha.pdf) 1 skipped (Delta.pdf)", res)
	}
	if got := readFile(t, filepath.Join(dir, "a.pdf")); got != "alpha" {
		t.Errorf("a.pdf content = %q", got)
	}
}

func TestUndoTolerantOfTruncatedLine(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "Alpha.pdf"), "alpha")
	journal := filepath.Join(dir, rename.JournalName)
	writeFile(t, journal,
		`{"t":"2023-01-15T10:00:00Z","old":"a.pdf","new":"Alpha.pdf"}`+"\n"+
			`{"t":"2023-01-15T10:00:01Z","old":"b.pdf","ne`)

	entries, err := rename.Read(dir, nil)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(entries) != 1 || entries[0].New != "Alpha.pdf" {
		t.Fatalf("Read = %+v, want one Alpha.pdf entry", entries)
	}

	res, err := rename.Undo(dir, false, nil)
	if err != nil {
		t.Fatalf("Undo: %v", err)
	}
	if res.Reverted != 1 {
		t.Errorf("Undo = %+v, want 1 reverted", res)
	}
	if got := listing(t, dir); !slices.Equal(got, []string{"a.pdf"}) {
		t.Errorf("listing = %v, want [a.pdf]", got)
	}
}

func TestUndoTwiceIsNoOp(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a.pdf")
	writeFile(t, src, "a")

	j, err := rename.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	it := rename.Item{OldPath: src, NewName: "b.pdf", Action: rename.ActionRename}
	if _, err := rename.Apply(context.Background(), dir, it, j); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if err := j.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if _, err := rename.Undo(dir, false, nil); err != nil {
		t.Fatalf("first Undo: %v", err)
	}
	if _, err := rename.Undo(dir, false, nil); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("second Undo err = %v, want fs.ErrNotExist", err)
	}
	if got := listing(t, dir); !slices.Equal(got, []string{"a.pdf"}) {
		t.Errorf("listing = %v, want [a.pdf]", got)
	}
	if got := readFile(t, src); got != "a" {
		t.Errorf("content = %q, want %q", got, "a")
	}
}

func TestUndoDryRunChangesNothing(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a.pdf")
	writeFile(t, src, "a")

	j, err := rename.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	it := rename.Item{OldPath: src, NewName: "b.pdf", Action: rename.ActionRename}
	if _, err := rename.Apply(context.Background(), dir, it, j); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if err := j.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	res, err := rename.Undo(dir, true, nil)
	if err != nil {
		t.Fatalf("Undo: %v", err)
	}
	if res.Reverted != 1 {
		t.Errorf("Undo = %+v, want 1 reverted", res)
	}
	want := []string{rename.JournalName, "b.pdf"}
	slices.Sort(want)
	if got := listing(t, dir); !slices.Equal(got, want) {
		t.Errorf("listing = %v, want %v", got, want)
	}
}
