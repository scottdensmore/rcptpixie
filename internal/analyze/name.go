package analyze

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/scottdensmore/rcptpixie/internal/rename"
)

type Receipt struct {
	StartDate time.Time
	EndDate   time.Time
	Total     float64
	Vendor    string
	Category  string
}

type Subject struct {
	Date  time.Time
	Title string
}

// ReceiptName reproduces the historical receipt format byte for byte, including
// the space-to-underscore substitution, and preserves the original extension.
func ReceiptName(r Receipt, ext string) string {
	datePart := r.StartDate.Format("01-02-2006")
	if !r.EndDate.IsZero() && !r.StartDate.Equal(r.EndDate) {
		datePart += " to " + r.EndDate.Format("01-02-2006")
	}
	// Sanitize first, then substitute spaces: the reverse order is what turned
	// "Food, Drink" into "Food,__Drink".
	vendorPart := underscoreSpaces(rename.SanitizeComponent(r.Vendor))
	categoryPart := underscoreSpaces(rename.SanitizeComponent(r.Category))
	stem := fmt.Sprintf("%s - %.2f - %s - %s", datePart, r.Total, vendorPart, categoryPart)
	return rename.SanitizeFilename(stem, ext)
}

func underscoreSpaces(s string) string { return strings.ReplaceAll(s, " ", "_") }

const MaxTitleRunes = 60

var leadingDateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}\s*-\s*`)

// SubjectName produces "YYYY-MM-DD - Descriptive Subject.ext". Spaces are kept:
// organize mode renames documents in place for humans to read.
func SubjectName(s Subject, ext string) string {
	title := rename.SanitizeComponent(s.Title)
	for leadingDateRe.MatchString(title) {
		title = leadingDateRe.ReplaceAllString(title, "")
	}
	if ext != "" && len(title) > len(ext) && strings.EqualFold(title[len(title)-len(ext):], ext) {
		title = title[:len(title)-len(ext)]
	}
	title = truncateTitle(title, MaxTitleRunes)
	if title == "" {
		title = "Untitled"
	}
	stem := s.Date.Format("2006-01-02") + " - " + title
	return rename.SanitizeFilename(stem, ext)
}

// truncateTitle cuts on a word boundary so a long title does not end mid-word.
func truncateTitle(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	cut := len(s)
	n := 0
	for i := range s {
		if n == max {
			cut = i
			break
		}
		n++
	}
	prefix := s[:cut]
	seg := prefix
	if i := strings.LastIndexByte(seg, ' '); i > 0 {
		seg = seg[:i]
	}
	// A title whose first word is punctuation (", 000...") trims away to nothing,
	// and a stem of just the date is both meaningless and not IsOrganized, so it
	// would be sent to the model again on every run. Each fallback keeps more.
	if t := strings.TrimRight(seg, " -_.,"); t != "" {
		return t
	}
	if t := strings.TrimRight(prefix, " -_.,"); t != "" {
		return t
	}
	return s
}

var organizedRe = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}) - .+`)

// IsOrganized reports whether base already follows the organize convention and
// is a fixed point of the sanitizer. Both clauses matter: the pattern alone
// accepts a stem padded with a zero-width joiner or a trailing NBSP, which would
// be skipped as "already good" while still being an unsafe name.
func IsOrganized(base string) bool {
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	m := organizedRe.FindStringSubmatch(stem)
	if m == nil {
		return false
	}
	d, err := time.Parse("2006-01-02", m[1])
	if err != nil || d.Format("2006-01-02") != m[1] {
		return false
	}
	return rename.SanitizeFilename(stem, ext) == base
}
