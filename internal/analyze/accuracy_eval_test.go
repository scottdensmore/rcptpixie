//go:build eval

// Package-level note: these tests run against a REAL Ollama and a real model.
// They are opt-in behind the `eval` tag because they need a server, take minutes
// and are not deterministic. They measure rather than assert: the point is to
// make a prompt change provable instead of plausible.
//
//	docker run -d --name rcptpixie-ollama -p 11434:11434 -v ollama-models:/root/.ollama ollama/ollama
//	docker exec rcptpixie-ollama ollama pull gemma4:e2b
//	go test -tags eval ./internal/analyze -run TestExtractionAccuracy -v -timeout 60m
package analyze_test

import (
	"context"
	"flag"
	"fmt"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/scottdensmore/rcptpixie/v2/internal/analyze"
	"github.com/scottdensmore/rcptpixie/v2/internal/doc"
	"github.com/scottdensmore/rcptpixie/v2/internal/ollama"
)

var (
	evalModel = flag.String("eval.model", ollama.DefaultModel, "model to evaluate")
	evalHost  = flag.String("eval.host", ollama.DefaultHost, "ollama host")
	evalOnly  = flag.String("eval.only", "", "run only samples whose name contains this")
	evalPath  = flag.String("eval.path", "both", "text, scan or both")
)

// result is one field-by-field comparison against ground truth.
type result struct {
	sample                           string
	path                             string
	dateOK, endOK, totalOK, vendorOK bool
	err                              error
	got                              analyze.Receipt
	elapsed                          time.Duration
}

func TestExtractionAccuracy(t *testing.T) {
	dir := t.TempDir()
	built := buildCorpus(t, dir)

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

	var results []result
	for _, s := range corpus {
		if *evalOnly != "" && !strings.Contains(s.Name, *evalOnly) {
			continue
		}
		for _, path := range []string{"text", "scan"} {
			if *evalPath != "both" && *evalPath != path {
				continue
			}
			file := built[s.Name].text
			if path == "scan" {
				file = built[s.Name].scan
			}
			results = append(results, evaluate(t, ctx, an, raster, s, path, file))
		}
	}
	report(t, results)
}

func evaluate(t *testing.T, ctx context.Context, an *analyze.Analyzer, r doc.Rasterizer, s sample, path, file string) result {
	t.Helper()
	res := result{sample: s.Name, path: path}
	start := time.Now()

	d, err := doc.Load(ctx, file, r, nil)
	if err != nil {
		res.err = fmt.Errorf("load: %w", err)
		res.elapsed = time.Since(start)
		return res
	}
	// A text-layer file that reached the vision path (or the reverse) would make
	// the comparison meaningless, so assert the routing actually happened.
	switch {
	case path == "text" && d.Kind != doc.KindText:
		res.err = fmt.Errorf("expected the text path, got %v", d.Kind)
		return res
	case path == "scan" && d.Kind != doc.KindImages:
		res.err = fmt.Errorf("expected the vision path, got %v", d.Kind)
		return res
	}

	got, err := an.Receipt(ctx, d)
	res.elapsed = time.Since(start)
	if err != nil {
		res.err = err
		return res
	}
	res.got = got
	res.dateOK = iso(got.StartDate) == s.StartDate
	res.endOK = iso(got.EndDate) == s.EndDate
	res.totalOK = math.Abs(got.Total-s.Total) < 0.005
	res.vendorOK = strings.Contains(strings.ToUpper(got.Vendor), strings.ToUpper(s.Vendor))
	return res
}

func iso(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02")
}

func report(t *testing.T, results []result) {
	t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "\n%-16s %-5s %-6s %-6s %-6s %-6s %8s  %s\n",
		"SAMPLE", "PATH", "DATE", "END", "TOTAL", "VENDOR", "TIME", "DETAIL")

	tally := map[string]*struct{ n, date, end, total, vendor, failed int }{}
	for _, r := range results {
		if tally[r.path] == nil {
			tally[r.path] = &struct{ n, date, end, total, vendor, failed int }{}
		}
		agg := tally[r.path]
		agg.n++

		if r.err != nil {
			agg.failed++
			fmt.Fprintf(&b, "%-16s %-5s %-6s %-6s %-6s %-6s %8s  ERROR: %v\n",
				r.sample, r.path, "-", "-", "-", "-", r.elapsed.Round(time.Millisecond), r.err)
			continue
		}
		if r.dateOK {
			agg.date++
		}
		if r.endOK {
			agg.end++
		}
		if r.totalOK {
			agg.total++
		}
		if r.vendorOK {
			agg.vendor++
		}

		var detail []string
		if !r.dateOK {
			detail = append(detail, "date="+iso(r.got.StartDate))
		}
		if !r.endOK {
			detail = append(detail, "end="+iso(r.got.EndDate))
		}
		if !r.totalOK {
			detail = append(detail, fmt.Sprintf("total=%.2f", r.got.Total))
		}
		if !r.vendorOK {
			detail = append(detail, "vendor="+r.got.Vendor)
		}
		fmt.Fprintf(&b, "%-16s %-5s %-6s %-6s %-6s %-6s %8s  %s\n",
			r.sample, r.path, mark(r.dateOK), mark(r.endOK), mark(r.totalOK), mark(r.vendorOK),
			r.elapsed.Round(time.Millisecond), strings.Join(detail, " "))
	}

	b.WriteString("\nSUMMARY\n")
	for _, path := range []string{"text", "scan"} {
		a := tally[path]
		if a == nil {
			continue
		}
		fmt.Fprintf(&b, "  %-5s n=%-3d date %2d/%-3d end %2d/%-3d total %2d/%-3d vendor %2d/%-3d errors %d\n",
			path, a.n, a.date, a.n, a.end, a.n, a.total, a.n, a.vendor, a.n, a.failed)
	}
	t.Log(b.String())

	// Written out so a run can be diffed against a later one.
	if out := os.Getenv("EVAL_OUT"); out != "" {
		if err := os.WriteFile(out, []byte(b.String()), 0o644); err != nil {
			t.Errorf("writing %s: %v", out, err)
		}
	}
}

func mark(ok bool) string {
	if ok {
		return "ok"
	}
	return "MISS"
}
