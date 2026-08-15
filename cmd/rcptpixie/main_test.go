package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/scottdensmore/rcptpixie/internal/testutil"
)

// binary is the compiled CLI under test. These tests exec it rather than call
// into it: the shipped bug lived in main()'s wiring, which an in-process test of
// a helper function could not reach.
var binary string

const testModel = "gemma4:e2b"

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "rcptpixie-bin")
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot create a build directory: %v\n", err)
		os.Exit(1)
	}
	// "go build -o" writes the path verbatim, and Windows will not exec a file
	// without a PATHEXT suffix — without this every test here fails on the
	// windows matrix job.
	binary = filepath.Join(dir, "rcptpixie")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-o", binary, ".")
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "go build failed: %v\n%s", err, out)
		os.RemoveAll(dir)
		os.Exit(1)
	}

	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func run(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.Command(binary, args...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	var exit *exec.ExitError
	if err := cmd.Run(); err != nil && !errors.As(err, &exit) {
		t.Fatalf("running %v: %v\nstderr:\n%s", args, err, errb.String())
	}
	return out.String(), errb.String(), cmd.ProcessState.ExitCode()
}

// useFake points the child process at an in-process fake ollama and pins the
// model, so a developer's real OLLAMA_HOST cannot change the result.
func useFake(t *testing.T, replies ...string) *testutil.Fake {
	t.Helper()
	f := testutil.NewFake(t, []string{testModel}, replies...)
	t.Setenv("RCPTPIXIE_HOST", f.URL)
	t.Setenv("RCPTPIXIE_MODEL", testModel)
	t.Setenv("OLLAMA_HOST", f.URL)
	return f
}

const receiptReply = `{"is_hotel":false,"vendor":"Test Store","date":"2023-01-15","end_date":"","total":123.45,"category":"Food"}`

const subjectReply = `{"date":"2024-03-11","subject":"Comcast Internet Service Invoice"}`

// receiptPDF puts a PDF with a real text layer at dir/name. It copies the
// committed fixture rather than synthesising one: a hand-rolled PDF landed under
// doc.MinTextChars once the whitespace was discounted, which sent these tests
// down the rasterizer path and made them depend on poppler being installed.
// They are here to exercise the CLI wiring, not the vision fallback.
func receiptPDF(t *testing.T, dir, name string) string {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("..", "..", "testdata", "receipt-text.pdf"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, src, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func exists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	return err == nil
}

// TestBarePathReachesReceipts is THE regression test: the shipped binary read a
// FlagSet it never parsed, so `rcptpixie file.pdf` printed usage and exited 0
// having done nothing at all.
func TestBarePathReachesReceipts(t *testing.T) {
	useFake(t, receiptReply)
	dir := t.TempDir()
	path := receiptPDF(t, dir, "receipt.pdf")

	stdout, stderr, code := run(t, path)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if strings.Contains(stdout, "Usage:") || strings.Contains(stderr, "Usage:") {
		t.Errorf("a bare path printed usage instead of processing the file\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	want := filepath.Join(dir, "01-15-2023 - 123.45 - Test_Store - Food.pdf")
	if !exists(t, want) {
		t.Errorf("%s was not created; the run exited 0 having done nothing\nstdout:\n%s\nstderr:\n%s",
			filepath.Base(want), stdout, stderr)
	}
	if exists(t, path) {
		t.Errorf("%s is still there, so nothing was renamed", path)
	}
	if !strings.Contains(stdout, "Renamed:") {
		t.Errorf("stdout does not report a rename:\n%s", stdout)
	}
}

func TestSubcommandPathIsReceived(t *testing.T) {
	useFake(t, subjectReply)
	dir := t.TempDir()
	path := filepath.Join(dir, "scan.txt")
	if err := os.WriteFile(path, []byte("Comcast internet statement for March 2024.\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	stdout, stderr, code := run(t, "organize", "-y", dir)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	want := filepath.Join(dir, "2024-03-11 - Comcast Internet Service Invoice.txt")
	if !exists(t, want) {
		t.Errorf("organize did not receive %s\nstdout:\n%s\nstderr:\n%s", dir, stdout, stderr)
	}
}

func TestFlagBeforeBarePath(t *testing.T) {
	useFake(t, receiptReply)
	dir := t.TempDir()
	path := receiptPDF(t, dir, "receipt.pdf")

	stdout, stderr, code := run(t, "-verbose", path)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !exists(t, filepath.Join(dir, "01-15-2023 - 123.45 - Test_Store - Food.pdf")) {
		t.Errorf("-verbose before a bare path did not reach receipts mode\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
}

func TestNoArgsIsUsageError(t *testing.T) {
	stdout, stderr, code := run(t)
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr, "Usage:") {
		t.Errorf("stderr has no usage:\n%s", stderr)
	}
	if stdout != "" {
		t.Errorf("stdout should be empty, got:\n%s", stdout)
	}
}

func TestHelpExitsZero(t *testing.T) {
	for _, arg := range []string{"-h", "help"} {
		t.Run(arg, func(t *testing.T) {
			stdout, stderr, code := run(t, arg)
			if code != 0 {
				t.Errorf("exit code = %d, want 0\nstderr:\n%s", code, stderr)
			}
			if !strings.Contains(stdout, "Usage:") {
				t.Errorf("usage did not go to stdout:\n%s", stdout)
			}
			if stderr != "" {
				t.Errorf("stderr should be empty, got:\n%s", stderr)
			}
		})
	}
}

var versionLine = regexp.MustCompile(`^rcptpixie version \S+ \(commit: `)

func TestVersionFlagAndSubcommandMatch(t *testing.T) {
	flagOut, stderr, code := run(t, "-version")
	if code != 0 {
		t.Fatalf("-version exit code = %d, want 0\nstderr:\n%s", code, stderr)
	}
	subOut, _, code := run(t, "version")
	if code != 0 {
		t.Fatalf("version exit code = %d, want 0", code)
	}
	if flagOut != subOut {
		t.Errorf("-version printed %q but version printed %q", flagOut, subOut)
	}
	// A typo in the goreleaser ldflags path leaves the fields at their defaults
	// without changing the shape, so the prefix is asserted as well.
	if !versionLine.MatchString(flagOut) {
		t.Errorf("version line has the wrong shape: %q", flagOut)
	}
}

func TestUnknownFlagIsUsageError(t *testing.T) {
	stdout, stderr, code := run(t, "-nope")
	if code != 2 {
		t.Errorf("exit code = %d, want 2\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	// ExitOnError would have called os.Exit(2) from inside Parse before the
	// message below could be printed.
	if !strings.Contains(stderr, "-nope") {
		t.Errorf("stderr does not name the bad flag:\n%s", stderr)
	}
}

func TestOllamaUnreachable(t *testing.T) {
	useFake(t, receiptReply)
	dir := t.TempDir()
	path := receiptPDF(t, dir, "receipt.pdf")

	stdout, stderr, code := run(t, "-host", "http://127.0.0.1:1", path)
	if code != 1 {
		t.Errorf("exit code = %d, want 1\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "ollama serve") {
		t.Errorf("stderr does not say how to start ollama:\n%s", stderr)
	}
	if !exists(t, path) {
		t.Errorf("%s was renamed even though ollama was unreachable", path)
	}
}

// TestStdoutStderrSeparation keeps `rcptpixie ... | xargs` usable: logs, warnings
// and the summary belong on stderr.
func TestStdoutStderrSeparation(t *testing.T) {
	useFake(t, receiptReply)
	dir := t.TempDir()
	receiptPDF(t, dir, "receipt.pdf")

	stdout, stderr, code := run(t, "receipts", "-verbose", dir)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	for _, line := range strings.Split(stdout, "\n") {
		for _, level := range []string{"debug:", "info:", "warn:", "error:"} {
			if strings.Contains(line, level) {
				t.Errorf("log line on stdout: %q", line)
			}
		}
	}
	if !strings.Contains(stderr, "debug:") {
		t.Errorf("-verbose logged nothing to stderr:\n%s", stderr)
	}
	if !strings.Contains(stdout, "Renamed:") {
		t.Errorf("results are missing from stdout:\n%s", stdout)
	}
}
