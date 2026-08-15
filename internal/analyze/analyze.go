package analyze

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/scottdensmore/rcptpixie/internal/doc"
	"github.com/scottdensmore/rcptpixie/internal/ollama"
)

type Analyzer struct {
	C     *ollama.Client
	Model string
	Log   *slog.Logger
}

const (
	receiptPredict = 300
	subjectPredict = 200

	// num_ctx must be explicit: Ollama defaults to 4096 and silently truncates
	// the prompt from the beginning, which would delete the instructions.
	textCtx   = 8192
	visionCtx = 16384

	// firstSeed is fixed so repeated runs over the same document agree; the retry
	// must use a different one because greedy decoding would otherwise reproduce
	// the identical unusable reply.
	firstSeed = 42
	retrySeed = 43

	maxRawInError = 2048

	// maxStay is the longest hotel range accepted before the check-out date is
	// treated as a misread.
	maxStay = 62 * 24 * time.Hour
)

var ErrNoSubject = errors.New("model produced no usable subject")

var (
	errNoVendor = errors.New("could not determine vendor")
	errNoDate   = errors.New("could not determine date")
)

type UnparseableError struct {
	Path string
	Raw  string
}

func (e *UnparseableError) Error() string {
	return fmt.Sprintf("model did not return usable JSON for %s: %s", e.Path, condense(e.Raw, 200))
}

type receiptWire struct {
	IsHotel  bool   `json:"is_hotel"`
	Vendor   string `json:"vendor"`
	Date     string `json:"date"`
	EndDate  string `json:"end_date"`
	Total    number `json:"total"`
	Category string `json:"category"`
}

type subjectWire struct {
	Date    string `json:"date"`
	Subject string `json:"subject"`
}

// number keeps the total as written so a model that answers "1,234.56" as a
// string is still recoverable through parseMoney.
type number string

func (n *number) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	switch {
	case s == "null":
		*n = ""
	case strings.HasPrefix(s, `"`):
		var str string
		if err := json.Unmarshal(b, &str); err != nil {
			return err
		}
		*n = number(str)
	default:
		// A bare JSON number is exact and the schema permits an exponent, so it is
		// reformatted here rather than left for parseMoney, whose digit filter
		// would splice "1.5e3" into 1.53.
		if v, err := strconv.ParseFloat(s, 64); err == nil {
			*n = number(strconv.FormatFloat(v, 'f', -1, 64))
			break
		}
		*n = number(s)
	}
	return nil
}

func (a *Analyzer) Receipt(ctx context.Context, d *doc.Doc) (Receipt, error) {
	var w receiptWire
	if err := a.generateJSON(ctx, kindReceipt, receiptSystem, ReceiptSchema, d, receiptPredict, &w); err != nil {
		return Receipt{}, err
	}
	return a.receiptFrom(w, d)
}

func (a *Analyzer) Subject(ctx context.Context, d *doc.Doc) (Subject, error) {
	var w subjectWire
	if err := a.generateJSON(ctx, kindDocument, organizeSystem, OrganizeSchema, d, subjectPredict, &w); err != nil {
		return Subject{}, err
	}
	return a.subjectFrom(w, d)
}

func (a *Analyzer) generateJSON(ctx context.Context, kind, system string, schema json.RawMessage, d *doc.Doc, numPredict int, v any) error {
	numCtx := textCtx
	if len(d.Images) > 0 {
		numCtx = visionCtx
	}

	prompt := buildPrompt(kind, d)
	var raw string
	for _, seed := range []int{firstSeed, retrySeed} {
		out, err := a.C.Generate(ctx, ollama.GenerateRequest{
			Model:  a.Model,
			System: system,
			Prompt: prompt,
			Images: d.Images,
			Format: schema,
			Stream: false,
			Options: map[string]any{
				"temperature": 0,
				"top_p":       1,
				"top_k":       1,
				"seed":        seed,
				"num_predict": numPredict,
				"num_ctx":     numCtx,
			},
		})
		if err != nil {
			return err
		}
		raw = out

		derr := decodeJSON(out, v)
		if derr == nil {
			return nil
		}
		if seed == retrySeed {
			break
		}
		a.log().Warn("model reply was not usable JSON, retrying once", "path", d.Path, "err", derr)
		prompt = fmt.Sprintf("Your previous reply could not be used: %s. Reply with ONLY a JSON object and no other text.\n\n%s",
			derr, buildPrompt(kind, d))
	}
	return &UnparseableError{Path: d.Path, Raw: condense(raw, maxRawInError)}
}

var (
	thinkRe = regexp.MustCompile(`(?is)<think>.*?</think>`)
	fenceRe = regexp.MustCompile("```[a-zA-Z0-9]*")
	expRe   = regexp.MustCompile(`[0-9][eE][-+]?[0-9]`)
)

// completer reports whether a decoded reply carries the one field its caller
// cannot work without, so a decoy object earlier in the reply loses to the real
// answer further down.
type completer interface{ complete() bool }

func (w *receiptWire) complete() bool { return strings.TrimSpace(w.Vendor) != "" }
func (w *subjectWire) complete() bool { return strings.TrimSpace(w.Subject) != "" }

func decodeJSON(raw string, v any) error {
	// A failed Unmarshal leaves whatever it managed to set behind, so v is reset
	// before every attempt: otherwise a field from a discarded object could
	// survive into the accepted result.
	reset(v)
	s := fenceRe.ReplaceAllString(thinkRe.ReplaceAllString(raw, ""), "")

	// A model that narrates before answering writes decoys ("for example {}")
	// ahead of the real object, so every balanced span is tried and the first one
	// carrying the caller's field wins. Stopping at the first span instead would
	// fail the file AND skip the retry that exists for exactly this reply.
	var best string
	var found bool
	var badJSON error
	for rest := s; ; {
		span, tail, ok := nextJSONObject(rest)
		if !ok {
			break
		}
		rest = tail
		if err := json.Unmarshal([]byte(span), v); err != nil {
			reset(v)
			if badJSON == nil {
				badJSON = err
			}
			continue
		}
		if !found {
			best, found = span, true
		}
		if c, ok := v.(completer); !ok || c.complete() {
			best = span
			break
		}
		reset(v)
	}
	if !found {
		if badJSON != nil {
			return fmt.Errorf("the reply was not valid JSON: %w", badJSON)
		}
		return errors.New("the reply contained no JSON object")
	}
	reset(v)
	return json.Unmarshal([]byte(best), v)
}

func reset(v any) {
	if rv := reflect.ValueOf(v); rv.Kind() == reflect.Pointer && !rv.IsNil() {
		rv.Elem().SetZero()
	}
}

// nextJSONObject returns the next balanced top-level {...} span in s along with
// the text following it. String literals and their escapes are tracked so a
// brace inside a vendor name does not end the scan.
func nextJSONObject(s string) (span, rest string, ok bool) {
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return "", "", false
	}
	depth, inStr, esc := 0, false, false
	for i := start; i < len(s); i++ {
		c := s[i]
		switch {
		case esc:
			esc = false
		case inStr && c == '\\':
			esc = true
		case c == '"':
			inStr = !inStr
		case inStr:
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				return s[start : i+1], s[i+1:], true
			}
		}
	}
	return "", "", false
}

func (a *Analyzer) receiptFrom(w receiptWire, d *doc.Doc) (Receipt, error) {
	r := Receipt{
		Vendor:   strings.TrimSpace(w.Vendor),
		Category: strings.TrimSpace(w.Category),
	}
	if r.Vendor == "" {
		return Receipt{}, errNoVendor
	}

	start, ok := parseDate(w.Date)
	if !ok || !plausibleDate(start) {
		return Receipt{}, errNoDate
	}
	end := start
	if w.IsHotel {
		if e, ok := parseDate(w.EndDate); ok && plausibleDate(e) {
			end = e
		}
	}
	if end.Before(start) {
		// A folio printing Departure above Arrival, or a vision read of the two
		// dates in the wrong order, otherwise yields a backwards name.
		a.log().Warn("hotel dates arrived reversed, swapping", "path", d.Path,
			"check_in", end.Format("2006-01-02"), "check_out", start.Format("2006-01-02"))
		start, end = end, start
	}
	if end.Sub(start) > maxStay {
		// Measured: on the vision path the model reads the check-in correctly and
		// then invents the check-out month. A folio spanning months is far rarer
		// than that misread, so the range is dropped rather than trusted.
		a.log().Warn("implausible stay length, using the check-in date alone", "path", d.Path,
			"check_in", start.Format("2006-01-02"), "check_out", end.Format("2006-01-02"))
		end = start
	}
	r.StartDate, r.EndDate = start, end

	// Presence is tracked by a bool, never by == 0, so a comped 0.00 folio is a
	// valid total rather than a missing one.
	hasTotal := false
	if s := strings.TrimSpace(string(w.Total)); s == "" {
		a.log().Warn("no total in the model output, using 0.00", "path", d.Path)
	} else if v, err := parseMoney(s); err != nil {
		a.log().Warn("could not read the total, using 0.00", "path", d.Path, "total", s, "err", err)
	} else {
		r.Total, hasTotal = v, true
	}

	if r.Category == "" {
		r.Category = "Other"
	}
	a.log().Debug("receipt analyzed", "path", d.Path, "vendor", r.Vendor,
		"category", r.Category, "has_total", hasTotal, "hotel", w.IsHotel)
	return r, nil
}

// subjectFrom leaves Date zero when the document states none; the caller falls
// back to the file's modification time.
func (a *Analyzer) subjectFrom(w subjectWire, d *doc.Doc) (Subject, error) {
	s := Subject{Title: strings.TrimSpace(w.Subject)}
	if s.Title == "" {
		return Subject{}, ErrNoSubject
	}
	if t, ok := parseDate(w.Date); ok && plausibleDate(t) {
		s.Date = t
	} else {
		a.log().Debug("no usable date in the model output", "path", d.Path, "date", w.Date)
	}
	return s, nil
}

var dateLayouts = []string{
	"2006-01-02",
	"2006/01/02",
	"01/02/2006",
	"1/2/2006",
	"Jan 2, 2006",
	time.RFC3339,
}

// parseDate never returns an error: an unreadable date is a missing date, and
// the caller decides whether that is fatal.
func parseDate(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range dateLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC), true
		}
	}
	return time.Time{}, false
}

// plausibleDate rejects the years a small model hallucinates when it cannot read
// the date at all.
func plausibleDate(t time.Time) bool {
	y := t.Year()
	return y >= 1970 && y <= time.Now().Year()+10
}

// parseMoney reads the totals the README has always claimed to support:
// "1,234.56", "$1,234.56", "1.234,56", "12.00 USD" and "(12.00)".
func parseMoney(s string) (float64, error) {
	t := strings.TrimSpace(s)
	if t == "" {
		return 0, errors.New("empty amount")
	}
	neg := strings.HasPrefix(t, "(") && strings.HasSuffix(t, ")")
	if expRe.MatchString(t) {
		// A printed total never uses exponent notation, so its meaning here is a
		// guess; refusing it loudly beats the filter below gluing the mantissa to
		// the exponent and writing a silently wrong amount into the filename.
		return 0, fmt.Errorf("exponent notation in amount %q", s)
	}

	var b strings.Builder
	for _, r := range t {
		switch {
		case r >= '0' && r <= '9', r == '.', r == ',':
			b.WriteRune(r)
		case r == '-' && b.Len() == 0:
			neg = true
		}
	}
	digits := b.String()
	if !strings.ContainsFunc(digits, func(r rune) bool { return r >= '0' && r <= '9' }) {
		return 0, fmt.Errorf("no digits in amount %q", s)
	}

	v, err := strconv.ParseFloat(normalizeSeparators(digits), 64)
	if err != nil {
		return 0, fmt.Errorf("unreadable amount %q", s)
	}
	if neg {
		v = -v
	}
	return v, nil
}

// normalizeSeparators resolves the '.' / ',' ambiguity: when both appear the
// last one is the decimal point, so US "1,234.56" and EU "1.234,56" both work.
func normalizeSeparators(s string) string {
	dot := strings.LastIndexByte(s, '.')
	comma := strings.LastIndexByte(s, ',')
	switch {
	case dot >= 0 && comma >= 0:
		if comma > dot {
			return decimalAt(s, ',')
		}
		return decimalAt(s, '.')
	case comma >= 0:
		if groupsOnly(s, ',') {
			return strings.ReplaceAll(s, ",", "")
		}
		return decimalAt(s, ',')
	case dot >= 0:
		// A lone '.' is a decimal point: "12.00" is far commoner than a European
		// "12.000". Two or more can only be thousands separators.
		if strings.Count(s, ".") > 1 && groupsOnly(s, '.') {
			return strings.ReplaceAll(s, ".", "")
		}
		return s
	}
	return s
}

func decimalAt(s string, sep byte) string {
	other := byte('.')
	if sep == '.' {
		other = ','
	}
	s = strings.ReplaceAll(s, string(other), "")
	i := strings.LastIndexByte(s, sep)
	return strings.ReplaceAll(s[:i], string(sep), "") + "." + s[i+1:]
}

// groupsOnly reports whether every sep in s is a thousands separator, i.e. is
// preceded by a digit and followed by exactly three digits.
func groupsOnly(s string, sep byte) bool {
	seen := false
	for i := 0; i < len(s); i++ {
		if s[i] != sep {
			continue
		}
		seen = true
		if i == 0 || !isDigit(s[i-1]) {
			return false
		}
		rest := s[i+1:]
		if j := strings.IndexByte(rest, sep); j >= 0 {
			rest = rest[:j]
		}
		if len(rest) != 3 || !allDigits(rest) {
			return false
		}
	}
	return seen
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if !isDigit(s[i]) {
			return false
		}
	}
	return s != ""
}

func condense(s string, max int) string {
	// Model output is driven by untrusted document text and lands on the user's
	// terminal through error and log lines, so every control rune becomes a space:
	// without the ESC and the C1 CSI, an escape sequence is inert prose.
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7F || (r >= 0x80 && r <= 0x9F) {
			return ' '
		}
		return r
	}, strings.ToValidUTF8(s, ""))
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > max {
		for max > 0 && !utf8.RuneStart(s[max]) {
			max--
		}
		s = s[:max] + "..."
	}
	return s
}

func (a *Analyzer) log() *slog.Logger {
	if a.Log != nil {
		return a.Log
	}
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
