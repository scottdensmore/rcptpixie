package version_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The linker accepts -X for a symbol that does not exist and says nothing: the
// build succeeds and the variable keeps its default. A release built with a
// stale path therefore reports "version dev" and no test, lint or CI step
// notices. The module path already moved once, for v2, so this pins the two
// files together.
func TestGoReleaserLdflagsMatchModulePath(t *testing.T) {
	module := modulePath(t)

	data, err := os.ReadFile(filepath.Join("..", ".goreleaser.yml"))
	if err != nil {
		t.Fatalf("read .goreleaser.yml: %v", err)
	}

	xFlag := regexp.MustCompile(`-X\s+([^\s=]+)=`)
	found := map[string]bool{}
	for _, m := range xFlag.FindAllStringSubmatch(string(data), -1) {
		target := m[1]
		// Split on the LAST dot: the import path itself contains dots.
		dot := strings.LastIndex(target, ".")
		if dot < 0 {
			t.Errorf("-X %q is not of the form <import path>.<var>", target)
			continue
		}
		pkg, field := target[:dot], target[dot+1:]
		if want := module + "/version"; pkg != want {
			t.Errorf("-X targets package %q, want %q\n"+
				"go.mod declares module %q; a mismatch silently ships a binary reporting \"version dev\"",
				pkg, want, module)
		}
		found[field] = true
	}

	// Every stamped variable must exist, or that -X is a no-op.
	for _, field := range []string{"Version", "Commit", "BuildDate"} {
		if !found[field] {
			t.Errorf(".goreleaser.yml does not stamp %s", field)
		}
	}
}

func modulePath(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "go.mod"))
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	for line := range strings.Lines(string(data)) {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.TrimSpace(rest)
		}
	}
	t.Fatal("go.mod has no module line")
	return ""
}
