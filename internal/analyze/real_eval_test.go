//go:build eval

package analyze_test

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/scottdensmore/rcptpixie/v2/internal/analyze"
	"github.com/scottdensmore/rcptpixie/v2/internal/doc"
	"github.com/scottdensmore/rcptpixie/v2/internal/ollama"
)

// The synthetic corpus is clean Helvetica rendered from PostScript. It found two
// real defects, but it cannot say how the tool handles a creased thermal receipt
// photographed under a desk lamp — which is the case the vision path exists for.
// This runs the same comparison over your own files.
//
//	mkdir -p testdata/real && cp ~/Receipts/* testdata/real/
//	go test -tags eval ./internal/analyze -run TestRealCorpus -eval.corpus=testdata/real -eval.scaffold
//	$EDITOR testdata/real/truth.json      # fill in what each receipt really says
//	go test -tags eval ./internal/analyze -run TestRealCorpus -eval.corpus=testdata/real -v
//
// testdata/real/ is gitignored. The receipts are read by the local Ollama and
// nothing else; nothing leaves the machine and nothing is committed.
var (
	evalCorpus   = flag.String("eval.corpus", "", "directory of real receipts to evaluate")
	evalScaffold = flag.Bool("eval.scaffold", false, "write a truth.json skeleton for -eval.corpus and stop")
)

// truth is what a receipt actually says. Dates are ISO; leave a field empty and
// it is not checked, so a receipt whose total you care about but whose date you
// cannot read still contributes.
type truth struct {
	Vendor  string  `json:"vendor"`
	Date    string  `json:"date"`
	EndDate string  `json:"end_date,omitempty"`
	Total   float64 `json:"total"`
	Skip    bool    `json:"skip,omitempty"`
	Note    string  `json:"note,omitempty"`
}

const truthFile = "truth.json"

func TestRealCorpusAccuracy(t *testing.T) {
	if *evalCorpus == "" {
		t.Skip("set -eval.corpus=<dir> to evaluate real receipts")
	}
	dir := resolveCorpus(t, *evalCorpus)
	files := corpusFiles(t, dir)
	if *evalScaffold {
		scaffold(t, dir, files)
		return
	}

	labels := loadTruth(t, dir)
	client, err := ollama.New(*evalHost, 10*time.Minute, nil)
	if err != nil {
		t.Fatalf("ollama.New: %v", err)
	}
	ctx := context.Background()
	if err := client.Preflight(ctx, *evalModel); err != nil {
		t.Skipf("no usable ollama at %s: %v", *evalHost, err)
	}
	an := &analyze.Analyzer{C: client, Model: *evalModel}
	raster := doc.Detect(nil)

	var (
		results    []result
		unlabelled []string
	)
	for _, f := range files {
		name := filepath.Base(f)
		want, ok := labels[name]
		if !ok {
			unlabelled = append(unlabelled, name)
			continue
		}
		if want.Skip || (*evalOnly != "" && !strings.Contains(name, *evalOnly)) {
			continue
		}
		results = append(results, evaluateReal(t, ctx, an, raster, name, f, want))
	}
	if len(unlabelled) > 0 {
		t.Logf("%d file(s) have no entry in %s and were not scored: %s",
			len(unlabelled), truthFile, strings.Join(unlabelled, ", "))
	}
	if len(results) == 0 {
		t.Skipf("nothing to score: add entries to %s", filepath.Join(dir, truthFile))
	}
	report(t, results)
}

// evaluateReal records the path the document actually took rather than forcing
// one: with real files, how many land on the vision path is itself a finding.
func evaluateReal(t *testing.T, ctx context.Context, an *analyze.Analyzer, r doc.Rasterizer, name, file string, want truth) result {
	t.Helper()
	res := result{sample: name, path: "?"}
	start := time.Now()

	d, err := doc.Load(ctx, file, r, nil)
	if err != nil {
		res.err = fmt.Errorf("load: %w", err)
		res.elapsed = time.Since(start)
		return res
	}
	res.path = "text"
	if d.Kind == doc.KindImages {
		res.path = "scan"
	}

	got, err := an.Receipt(ctx, d)
	res.elapsed = time.Since(start)
	if err != nil {
		res.err = err
		return res
	}
	res.got = got
	// An empty label means "not checked", so a partially labelled receipt still
	// contributes the fields you were sure about.
	res.dateOK = want.Date == "" || iso(got.StartDate) == want.Date
	res.endOK = want.EndDate == "" || iso(got.EndDate) == want.EndDate
	res.totalOK = want.Total == 0 || math.Abs(got.Total-want.Total) < 0.005
	res.vendorOK = want.Vendor == "" || strings.Contains(strings.ToUpper(got.Vendor), strings.ToUpper(want.Vendor))
	return res
}

// resolveCorpus lets -eval.corpus be written the way it reads in the docs.
// A test runs with the package directory as its working directory, so a plain
// "testdata/real" would otherwise mean internal/analyze/testdata/real.
func resolveCorpus(t *testing.T, path string) string {
	t.Helper()
	if filepath.IsAbs(path) {
		return path
	}
	if _, err := os.Stat(path); err == nil {
		return path
	}
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, path)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return path // let the caller report the original path
		}
		dir = parent
	}
}

func corpusFiles(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	var out []string
	for _, e := range ents {
		if e.IsDir() || e.Name() == truthFile || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if !loadable(e.Name()) {
			continue
		}
		out = append(out, filepath.Join(dir, e.Name()))
	}
	sort.Strings(out)
	if len(out) == 0 {
		t.Skipf("no supported files in %s", dir)
	}
	return out
}

// loadable mirrors what doc.Load accepts, so an unrelated file sitting in the
// folder is passed over rather than counted as a failure.
func loadable(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	if ext == ".pdf" || ext == ".txt" || ext == ".md" {
		return true
	}
	return slices.Contains(doc.ImageExts, ext)
}

func loadTruth(t *testing.T, dir string) map[string]truth {
	t.Helper()
	path := filepath.Join(dir, truthFile)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v\nrun with -eval.scaffold to create it", path, err)
	}
	var m map[string]truth
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	return m
}

// scaffold writes an entry per file so labelling is filling in blanks rather
// than authoring JSON. An existing truth.json is never overwritten.
func scaffold(t *testing.T, dir string, files []string) {
	t.Helper()
	path := filepath.Join(dir, truthFile)
	existing := map[string]truth{}
	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, &existing); err != nil {
			t.Fatalf("parsing the existing %s: %v", path, err)
		}
	}
	added := 0
	for _, f := range files {
		name := filepath.Base(f)
		if _, ok := existing[name]; ok {
			continue
		}
		existing[name] = truth{Note: "fill in vendor, date (YYYY-MM-DD), total; set skip to exclude"}
		added++
	}
	b, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s: %d new entr(ies), %d total. Fill them in, then re-run without -eval.scaffold.",
		path, added, len(existing))
}
