package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/scottdensmore/rcptpixie/internal/ollama"
	"github.com/scottdensmore/rcptpixie/internal/rename"
	"github.com/scottdensmore/rcptpixie/internal/testutil"
)

const testModel = "gemma4:e2b"

const receiptReply = `{"is_hotel":false,"vendor":"Test Store","date":"2023-01-15","end_date":"","total":123.45,"category":"Food"}`

func subjectReply(t *testing.T, date, subject string) string {
	t.Helper()
	b, err := json.Marshal(map[string]string{"date": date, "subject": subject})
	if err != nil {
		t.Fatalf("marshal reply: %v", err)
	}
	return string(b)
}

type result struct {
	code   ExitCode
	stdout string
	stderr string
}

func (r result) dump() string {
	return fmt.Sprintf("code=%d\nstdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
}

// runCLI drives Run entirely in process: no os.Args, no os.Exit, no real server.
func runCLI(t *testing.T, getenv func(string) string, stdin string, isTTY bool, args ...string) result {
	t.Helper()
	var out, errb bytes.Buffer
	code := Run(context.Background(), Env{
		Args:   args,
		Stdout: &out,
		Stderr: &errb,
		Stdin:  strings.NewReader(stdin),
		Getenv: getenv,
		IsTTY:  func(any) bool { return isTTY },
	})
	return result{code: code, stdout: out.String(), stderr: errb.String()}
}

func runFake(t *testing.T, f *testutil.Fake, args ...string) result {
	t.Helper()
	return runCLI(t, env(map[string]string{"RCPTPIXIE_HOST": f.URL}), "", false, args...)
}

func env(vals map[string]string) func(string) string {
	return func(k string) string { return vals[k] }
}

func newFake(t *testing.T, replies ...string) *testutil.Fake {
	t.Helper()
	return testutil.NewFake(t, []string{testModel}, replies...)
}

func writeFile(t *testing.T, path, content string) string {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func mkdir(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	return path
}

// listing returns dir's entry names, journal included: a test that ignored the
// journal could not tell an idempotent run from one that rewrote history.
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
	return names
}

func TestRoute(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantCmd  string
		wantRest []string
	}{
		{"explicit receipts", []string{"receipts", "dir"}, modeReceipts, []string{"dir"}},
		{"bare path", []string{"file.pdf"}, modeReceipts, []string{"file.pdf"}},
		{"leading flag", []string{"-verbose", "f.pdf"}, modeReceipts, []string{"-verbose", "f.pdf"}},
		{"terminator", []string{"--", "organize"}, modeReceipts, []string{"--", "organize"}},
		{"organize", []string{"organize", "d"}, modeOrganize, []string{"d"}},
		{"undo without a path", []string{"undo"}, modeUndo, nil},
		{"nothing", nil, "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, rest := route(tt.args)
			if cmd != tt.wantCmd {
				t.Errorf("cmd = %q, want %q", cmd, tt.wantCmd)
			}
			if !slices.Equal(rest, tt.wantRest) {
				t.Errorf("rest = %q, want %q", rest, tt.wantRest)
			}
		})
	}
}

// TestGlobalFlagCommandLineUntouched is the direct assertion for the shipped
// bug: main parsed a private FlagSet and then read the global flag.Args(), which
// was never parsed and so was always empty.
func TestGlobalFlagCommandLineUntouched(t *testing.T) {
	beforeFlags, beforeArgs := flag.NFlag(), len(flag.Args())

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "scan.txt"), "Comcast internet statement.\n")
	f := newFake(t, subjectReply(t, "2024-03-11", "Comcast Internet Service Invoice"))
	if got := runFake(t, f, "organize", "-y", "-model", testModel, dir); got.code != ExitOK {
		t.Fatalf("organize failed: %s", got.dump())
	}

	if flag.NFlag() != beforeFlags {
		t.Errorf("flag.NFlag() = %d, want %d: Run touched flag.CommandLine", flag.NFlag(), beforeFlags)
	}
	if len(flag.Args()) != beforeArgs {
		t.Errorf("len(flag.Args()) = %d, want %d: Run touched flag.CommandLine", len(flag.Args()), beforeArgs)
	}
}

func TestExitCodes(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		dir := t.TempDir()
		path := writeFile(t, filepath.Join(dir, "receipt.txt"), "Test Store\nTOTAL 123.45\n")
		f := newFake(t, receiptReply)
		if got := runFake(t, f, path); got.code != ExitOK {
			t.Errorf("code = %d, want %d\n%s", got.code, ExitOK, got.dump())
		}
	})

	t.Run("usage", func(t *testing.T) {
		f := newFake(t, receiptReply)
		if got := runFake(t, f, "receipts"); got.code != ExitUsage {
			t.Errorf("code = %d, want %d\n%s", got.code, ExitUsage, got.dump())
		}
	})

	t.Run("partial", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "good.txt"), "Comcast internet statement.\n")
		writeFile(t, filepath.Join(dir, "opaque.bin"), "\x00\x01\x02")
		f := newFake(t, subjectReply(t, "2024-03-11", "Comcast Internet Service Invoice"))
		got := runFake(t, f, "organize", "-y", dir)
		if got.code != ExitPartial {
			t.Errorf("code = %d, want %d\n%s", got.code, ExitPartial, got.dump())
		}
	})

	t.Run("failure", func(t *testing.T) {
		dir := t.TempDir()
		path := writeFile(t, filepath.Join(dir, "receipt.txt"), "Test Store\nTOTAL 123.45\n")
		got := runCLI(t, env(nil), "", false, "-host", "http://127.0.0.1:1", path)
		if got.code != ExitFailure {
			t.Errorf("code = %d, want %d\n%s", got.code, ExitFailure, got.dump())
		}
		if !strings.Contains(got.stderr, "ollama serve") {
			t.Errorf("stderr does not say how to start ollama:\n%s", got.stderr)
		}
	})
}

func TestEnvFallback(t *testing.T) {
	const envModel = "custom:tag"
	dir := t.TempDir()
	path := writeFile(t, filepath.Join(dir, "receipt.txt"), "Test Store\nTOTAL 123.45\n")

	f := testutil.NewFake(t, []string{envModel, testModel}, receiptReply)
	getenv := env(map[string]string{"OLLAMA_HOST": f.URL, "RCPTPIXIE_MODEL": envModel})

	if got := runCLI(t, getenv, "", false, path); got.code != ExitOK {
		t.Fatalf("run with env fallbacks: %s", got.dump())
	}
	if f.Count() != 1 {
		t.Fatalf("fake saw %d generate calls, want 1: OLLAMA_HOST was not honoured", f.Count())
	}
	if model := f.Requests[0]["model"]; model != envModel {
		t.Errorf("model = %v, want %q from RCPTPIXIE_MODEL", model, envModel)
	}

	// An explicit flag must beat the environment.
	f2 := testutil.NewFake(t, []string{envModel, testModel}, receiptReply)
	getenv2 := env(map[string]string{"OLLAMA_HOST": f2.URL, "RCPTPIXIE_MODEL": envModel})
	renamed := filepath.Join(dir, "01-15-2023 - 123.45 - Test_Store - Food.txt")
	if got := runCLI(t, getenv2, "", false, "-model", testModel, renamed); got.code != ExitOK {
		t.Fatalf("run with -model: %s", got.dump())
	}
	if model := f2.Requests[0]["model"]; model != testModel {
		t.Errorf("model = %v, want %q: -model lost to RCPTPIXIE_MODEL", model, testModel)
	}
}

func TestDefaultModelIsGemma4E2B(t *testing.T) {
	var o opts
	fs := newFlagSet(modeReceipts, io.Discard)
	o.register(fs, env(nil))
	if _, err := o.parseInto(fs, nil); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if o.Model != "gemma4:e2b" {
		t.Errorf("default model = %q, want %q", o.Model, "gemma4:e2b")
	}
	if ollama.DefaultModel != "gemma4:e2b" {
		t.Errorf("ollama.DefaultModel = %q, want %q", ollama.DefaultModel, "gemma4:e2b")
	}
}

// TestOrganizeIsIdempotent proves the already-organized skip fires BEFORE
// inference: a second pass over a folder must cost one directory listing, not
// another N model calls.
func TestOrganizeIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	const n = 20
	replies := make([]string, 0, n)
	for i := 0; i < n; i++ {
		writeFile(t, filepath.Join(dir, fmt.Sprintf("scan-%02d.txt", i)), fmt.Sprintf("statement number %d\n", i))
		replies = append(replies, subjectReply(t, fmt.Sprintf("2024-03-%02d", i+1), fmt.Sprintf("Acme Monthly Statement %02d", i)))
	}

	first := newFake(t, replies...)
	if got := runFake(t, first, "organize", "-y", dir); got.code != ExitOK {
		t.Fatalf("first run: %s", got.dump())
	}
	if first.Count() != n {
		t.Fatalf("first run made %d generate calls, want %d", first.Count(), n)
	}
	after := listing(t, dir)
	if slices.Contains(after, "scan-00.txt") {
		t.Fatalf("nothing was renamed: %q", after)
	}

	second := newFake(t, replies...)
	if got := runFake(t, second, "organize", "-y", dir); got.code != ExitOK {
		t.Fatalf("second run: %s", got.dump())
	}
	if second.Count() != 0 {
		t.Errorf("second run made %d generate calls, want 0", second.Count())
	}
	if got := listing(t, dir); !slices.Equal(got, after) {
		t.Errorf("second run changed the directory:\n got %q\nwant %q", got, after)
	}
}

func TestOrganizeNonTTYWithoutYes(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "scan.txt"), "Comcast internet statement.\n")
	before := listing(t, dir)

	f := newFake(t, subjectReply(t, "2024-03-11", "Comcast Internet Service Invoice"))
	got := runFake(t, f, "organize", dir)
	if got.code != ExitUsage {
		t.Errorf("code = %d, want %d\n%s", got.code, ExitUsage, got.dump())
	}
	if !strings.Contains(got.stdout, "PLAN") {
		t.Errorf("the plan was not printed:\n%s", got.stdout)
	}
	if now := listing(t, dir); !slices.Equal(now, before) {
		t.Errorf("files changed without confirmation: %q -> %q", before, now)
	}
}

func TestOrganizeDryRunChangesNothing(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "scan.txt"), "Comcast internet statement.\n")
	before := listing(t, dir)

	f := newFake(t, subjectReply(t, "2024-03-11", "Comcast Internet Service Invoice"))
	got := runFake(t, f, "organize", "-n", dir)
	if got.code != ExitOK {
		t.Errorf("code = %d, want %d\n%s", got.code, ExitOK, got.dump())
	}
	if !strings.Contains(got.stdout, "2024-03-11 - Comcast Internet Service Invoice.txt") {
		t.Errorf("the plan does not show the new name:\n%s", got.stdout)
	}
	if now := listing(t, dir); !slices.Equal(now, before) {
		t.Errorf("-n renamed something: %q -> %q", before, now)
	}
	if _, err := os.Stat(filepath.Join(dir, ".rcptpixie-undo.jsonl")); err == nil {
		t.Error("-n wrote an undo journal")
	}
}

// TestMaliciousModelOutput sends the model output a compromised or simply
// confused model would produce. Nothing may escape the working directory.
func TestMaliciousModelOutput(t *testing.T) {
	parent := t.TempDir()
	dir := mkdir(t, filepath.Join(parent, "work"))

	subjects := []string{
		"../../../etc/passwd",
		"..",
		"CON",
		"a‮gnp.exe",
		strings.Repeat("\U0001F600", 5000),
	}
	replies := make([]string, 0, len(subjects))
	for i, s := range subjects {
		writeFile(t, filepath.Join(dir, fmt.Sprintf("scan-%02d.txt", i)), "some document text\n")
		replies = append(replies, subjectReply(t, "2024-01-01", s))
	}

	f := newFake(t, replies...)
	got := runFake(t, f, "organize", "-y", dir)
	if got.code != ExitOK {
		t.Errorf("code = %d, want %d\n%s", got.code, ExitOK, got.dump())
	}
	if strings.Contains(got.stderr, "internal error") {
		t.Errorf("Run panicked:\n%s", got.stderr)
	}

	if outer := listing(t, parent); !slices.Equal(outer, []string{"work"}) {
		t.Errorf("something escaped the working directory: %q", outer)
	}
	names := listing(t, dir)
	if len(names) != len(subjects)+1 { // the files plus the undo journal
		t.Errorf("dir holds %d entries, want %d: %q", len(names), len(subjects)+1, names)
	}
	for _, name := range names {
		if name != filepath.Base(name) || strings.Contains(name, "..") || strings.ContainsAny(name, `/\`) {
			t.Errorf("unsafe name on disk: %q", name)
		}
		if len(name) > 255 {
			t.Errorf("name is %d bytes, over the filesystem limit: %q", len(name), name)
		}
	}
}

func TestCollectNonRecursiveSkipsSubdirs(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "top level\n")
	sub := mkdir(t, filepath.Join(dir, "sub"))
	writeFile(t, filepath.Join(sub, "b.txt"), "nested\n")

	paths, skipped, err := collect(dir, false, nil)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if want := []string{filepath.Join(dir, "a.txt")}; !slices.Equal(paths, want) {
		t.Errorf("paths = %q, want %q", paths, want)
	}
	if skipped != 1 {
		t.Errorf("skippedDirs = %d, want 1", skipped)
	}

	// Silent under-processing is the failure mode, so the skip must be reported.
	f := newFake(t, subjectReply(t, "2024-03-11", "Acme Monthly Statement"))
	got := runFake(t, f, "organize", "-n", dir)
	if !strings.Contains(got.stderr, "subdirectories skipped") {
		t.Errorf("no warning about the skipped subdirectory:\n%s", got.stderr)
	}

	paths, _, err = collect(dir, true, nil)
	if err != nil {
		t.Fatalf("collect recursive: %v", err)
	}
	if len(paths) != 2 {
		t.Errorf("-r collected %q, want both files", paths)
	}
}

func TestCollectRecursiveTolerantOfUnreadableEntry(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "top level\n")
	other := mkdir(t, filepath.Join(dir, "other"))
	writeFile(t, filepath.Join(other, "c.txt"), "nested\n")
	locked := mkdir(t, filepath.Join(dir, "locked"))
	writeFile(t, filepath.Join(locked, "hidden.txt"), "unreachable\n")
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o755) })

	paths, _, err := collect(dir, true, nil)
	if err != nil {
		t.Fatalf("one unreadable subdirectory aborted the whole walk: %v", err)
	}
	want := []string{filepath.Join(dir, "a.txt"), filepath.Join(other, "c.txt")}
	if !slices.Equal(paths, want) {
		t.Errorf("paths = %q, want %q", paths, want)
	}
}

func TestUndoRoundTripThroughCLI(t *testing.T) {
	dir := t.TempDir()
	replies := make([]string, 0, 3)
	for i := 0; i < 3; i++ {
		writeFile(t, filepath.Join(dir, fmt.Sprintf("scan-%02d.txt", i)), "some document text\n")
		replies = append(replies, subjectReply(t, "2024-03-11", fmt.Sprintf("Acme Monthly Statement %02d", i)))
	}
	before := listing(t, dir)

	f := newFake(t, replies...)
	if got := runFake(t, f, "organize", "-y", dir); got.code != ExitOK {
		t.Fatalf("organize: %s", got.dump())
	}
	if now := listing(t, dir); slices.Equal(now, before) {
		t.Fatalf("organize renamed nothing: %q", now)
	}

	if got := runFake(t, f, "undo", "-y", dir); got.code != ExitOK {
		t.Fatalf("undo: %s", got.dump())
	}
	if now := listing(t, dir); !slices.Equal(now, before) {
		t.Errorf("undo did not restore the directory:\n got %q\nwant %q", now, before)
	}
}

// Flags must work on either side of the path: `rcptpixie receipt.pdf -n` is how
// people actually type it, and stdlib flag stops parsing at the first non-flag.
func TestFlagsAfterPositional(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"flag after path", []string{"a.pdf", "-n"}},
		{"flag before path", []string{"-n", "a.pdf"}},
		{"value flag after path", []string{"a.pdf", "-model", "m:1", "-n"}},
		{"value flag with equals", []string{"a.pdf", "-model=m:1", "-n"}},
		{"interleaved", []string{"-v", "a.pdf", "-n"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var o opts
			fs := newFlagSet("receipts", io.Discard)
			o.register(fs, nil)
			rest, err := o.parseInto(fs, tc.args)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if len(rest) != 1 || rest[0] != "a.pdf" {
				t.Errorf("positionals = %q, want [a.pdf]", rest)
			}
			if !o.DryRun {
				t.Error("-n was not applied")
			}
		})
	}
}

// A "--" terminator must keep everything after it positional.
func TestDoubleDashStopsPermutation(t *testing.T) {
	var o opts
	fs := newFlagSet("receipts", io.Discard)
	o.register(fs, nil)
	rest, err := o.parseInto(fs, []string{"--", "-weird-name.pdf"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(rest) != 1 || rest[0] != "-weird-name.pdf" {
		t.Errorf("positionals = %q, want [-weird-name.pdf]", rest)
	}
}

// undo must accept a trailing flag exactly as the other two subcommands do: the
// tool's own hint is "rcptpixie undo <dir>", and people append -y to it.
func TestUndoAcceptsFlagsAfterPositional(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "scan.txt"), "Comcast internet statement.\n")
	before := listing(t, dir)

	f := newFake(t, subjectReply(t, "2024-03-11", "Comcast Internet Service Invoice"))
	if got := runFake(t, f, "organize", "-y", dir); got.code != ExitOK {
		t.Fatalf("organize: %s", got.dump())
	}

	if got := runFake(t, f, "undo", dir, "-n"); got.code != ExitOK {
		t.Fatalf("undo -n after the path: %s", got.dump())
	}
	if got := runFake(t, f, "undo", dir, "-y"); got.code != ExitOK {
		t.Fatalf("undo -y after the path: %s", got.dump())
	}
	if now := listing(t, dir); !slices.Equal(now, before) {
		t.Errorf("undo did not restore the directory:\n got %q\nwant %q", now, before)
	}
}

// A fifo named on the command line must be refused rather than opened: the open
// blocks until a writer appears and no timeout covers it. The same fifo inside a
// directory is already dropped by candidate().
func TestExplicitNonRegularFileIsRefused(t *testing.T) {
	if _, err := exec.LookPath("mkfifo"); err != nil {
		t.Skip("mkfifo is unavailable")
	}
	dir := t.TempDir()
	fifo := filepath.Join(dir, "p.pdf")
	if out, err := exec.Command("mkfifo", fifo).CombinedOutput(); err != nil {
		t.Skipf("mkfifo: %v: %s", err, out)
	}

	f := newFake(t, receiptReply)
	done := make(chan result, 1)
	go func() { done <- runFake(t, f, "-n", fifo) }()

	select {
	case got := <-done:
		if got.code != ExitUsage {
			t.Errorf("code = %d, want %d\n%s", got.code, ExitUsage, got.dump())
		}
		if !strings.Contains(got.stderr, "not a regular file") {
			t.Errorf("stderr does not explain the refusal:\n%s", got.stderr)
		}
		if f.Count() != 0 {
			t.Errorf("fake saw %d generate calls, want 0", f.Count())
		}
	case <-time.After(20 * time.Second):
		t.Fatal("rcptpixie blocked on the fifo instead of refusing it")
	}
}

// planFor builds a one-item plan over a file it creates in dir.
func planFor(t *testing.T, dir, old, newName string) *rename.Plan {
	t.Helper()
	path := writeFile(t, filepath.Join(dir, old), "some document text\n")
	return &rename.Plan{Dir: dir, Items: []rename.Item{{OldPath: path, NewName: newName, Action: rename.ActionRename}}}
}

// A run that renames nothing must leave no journal behind: an empty
// .rcptpixie-undo.jsonl is litter that "rcptpixie undo" then rejects.
func TestApplyPlanWritesNoEmptyJournal(t *testing.T) {
	log := newLogger(io.Discard, slog.LevelError)
	quiet := Env{Stdout: io.Discard, Stderr: io.Discard}

	t.Run("cancelled before the first rename", func(t *testing.T) {
		dir := t.TempDir()
		p := planFor(t, dir, "a.txt", "b.txt")
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if n := applyPlan(ctx, quiet, p, log); n != 0 {
			t.Fatalf("renamed = %d, want 0", n)
		}
		if got := listing(t, dir); !slices.Equal(got, []string{"a.txt"}) {
			t.Errorf("dir = %q, want just the untouched file", got)
		}
	})

	t.Run("every rename fails", func(t *testing.T) {
		dir := t.TempDir()
		p := planFor(t, dir, "a.txt", "..") // Apply refuses an unsafe name
		if n := applyPlan(context.Background(), quiet, p, log); n != 0 {
			t.Fatalf("renamed = %d, want 0", n)
		}
		if got := listing(t, dir); !slices.Equal(got, []string{"a.txt"}) {
			t.Errorf("dir = %q, want just the untouched file", got)
		}
	})

	// The counterpart: a journal holding an earlier run's history is not empty
	// and must survive a run that renames nothing.
	t.Run("keeps an earlier run's history", func(t *testing.T) {
		dir := t.TempDir()
		p := planFor(t, dir, "a.txt", "..")
		history := writeFile(t, filepath.Join(dir, rename.JournalName),
			`{"t":"2024-01-01T00:00:00Z","old":"x.txt","new":"y.txt"}`+"\n")
		applyPlan(context.Background(), quiet, p, log)
		b, err := os.ReadFile(history)
		if err != nil || !strings.Contains(string(b), "x.txt") {
			t.Errorf("earlier history was destroyed: err=%v content=%q", err, b)
		}
	})
}

// A second pass over a directory whose names already carry collision suffixes
// must report every file unchanged. Re-deriving "X (2).txt" for the file that
// already holds that name is not a rename, and printing it as one makes the tool
// look non-idempotent and inflates the count scripts read.
func TestReceiptsSecondRunOverCollisionSuffixes(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 3; i++ {
		writeFile(t, filepath.Join(dir, fmt.Sprintf("scan-%02d.txt", i)), "Test Store\nTOTAL 123.45\n")
	}

	first := newFake(t, receiptReply)
	if got := runFake(t, first, "-ext", ".txt", dir); got.code != ExitOK {
		t.Fatalf("first run: %s", got.dump())
	}
	after := listing(t, dir)
	want := []string{
		rename.JournalName,
		"01-15-2023 - 123.45 - Test_Store - Food (2).txt",
		"01-15-2023 - 123.45 - Test_Store - Food (3).txt",
		"01-15-2023 - 123.45 - Test_Store - Food.txt",
	}
	if !slices.Equal(after, want) {
		t.Fatalf("after the first run the dir holds %q, want %q", after, want)
	}

	second := newFake(t, receiptReply)
	got := runFake(t, second, "-ext", ".txt", dir)
	if got.code != ExitOK {
		t.Fatalf("second run: %s", got.dump())
	}
	if strings.Contains(got.stdout, "->") {
		t.Errorf("the plan reports a rename over files that already have their names:\n%s", got.stdout)
	}
	if strings.Contains(got.stdout, "Renamed:") {
		t.Errorf("the second run claims to have renamed something:\n%s", got.stdout)
	}
	if !strings.Contains(got.stderr, "0 to rename, 3 unchanged") {
		t.Errorf("counts are wrong:\n%s", got.stderr)
	}
	if now := listing(t, dir); !slices.Equal(now, after) {
		t.Errorf("the second run changed the directory:\n got %q\nwant %q", now, after)
	}
}

// An unknown flag must still be an error wherever it appears.
func TestUnknownFlagAfterPositionalStillErrors(t *testing.T) {
	var o opts
	fs := newFlagSet("receipts", io.Discard)
	o.register(fs, nil)
	if _, err := o.parseInto(fs, []string{"a.pdf", "-nope"}); err == nil {
		t.Error("expected an error for -nope")
	}
}

// A flag's VALUE must never be mistaken for a meta command. `-model help dir/`
// used to print usage and exit 0 having processed nothing — the same silent
// no-op the bare-path bug caused.
func TestFlagValueIsNotAMetaCommand(t *testing.T) {
	for _, args := range [][]string{
		{"-model", "help", "d"},
		{"-model", "version", "d"},
		{"-host", "help", "d"},
		{"-ext", "help", "d"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			if got := scanMeta(args); got != "" {
				t.Errorf("scanMeta(%q) = %q, want \"\"", args, got)
			}
		})
	}
}

// The genuine meta forms must still be recognised wherever they appear.
func TestMetaCommandsStillRecognised(t *testing.T) {
	cases := map[string][]string{
		"help":    {"help"},
		"version": {"version"},
	}
	for want, args := range cases {
		if got := scanMeta(args); got != want {
			t.Errorf("scanMeta(%q) = %q, want %q", args, got, want)
		}
	}
	for _, a := range []string{"-h", "-help", "--help"} {
		if got := scanMeta([]string{a, "d"}); got != "help" {
			t.Errorf("scanMeta(%q) = %q, want help", a, got)
		}
	}
	for _, a := range []string{"-version", "--version"} {
		if got := scanMeta([]string{a}); got != "version" {
			t.Errorf("scanMeta(%q) = %q, want version", a, got)
		}
	}
	if got := scanMeta([]string{"-model=help", "d"}); got != "" {
		t.Errorf("scanMeta with -model=help = %q, want \"\"", got)
	}
}
