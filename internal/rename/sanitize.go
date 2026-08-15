package rename

import (
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

// maxBaseLen is 255 minus 8 bytes reserved for a " (999)" collision suffix.
const maxBaseLen = 247

// trimCutset is stripped from both ends until the result is stable.
const trimCutset = ".-_ "

// winReserved are the MS-DOS device names that remain unusable as the first
// dot-segment of a Windows filename.
var winReserved = map[string]bool{
	"con": true, "prn": true, "aux": true, "nul": true,
	"com1": true, "com2": true, "com3": true, "com4": true, "com5": true,
	"com6": true, "com7": true, "com8": true, "com9": true,
	"lpt1": true, "lpt2": true, "lpt3": true, "lpt4": true, "lpt5": true,
	"lpt6": true, "lpt7": true, "lpt8": true, "lpt9": true,
}

// SanitizeComponent makes one model-derived string safe as part of a filename.
// Spaces are PRESERVED (callers decide underscore policy).
func SanitizeComponent(s string) string {
	s = dropUnsafeRunes(s)
	s = mapSeparators(s)
	s = collapseDots(s)
	s = normalizeSpaces(s)
	s = strings.Trim(s, trimCutset)
	if s == "" {
		return "Untitled"
	}
	return s
}

// dropUnsafeRunes discards invalid UTF-8 along with control, format, surrogate,
// private-use and unassigned runes. Bidi overrides and zero-width joiners are
// removed here so they cannot disguise an extension or pad a name invisibly.
func dropUnsafeRunes(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i, r := range s {
		if r == utf8.RuneError {
			if _, size := utf8.DecodeRuneInString(s[i:]); size == 1 {
				continue
			}
		}
		if isUnsafeRune(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func isUnsafeRune(r rune) bool {
	switch r {
	case 0x00AD, 0x2060, 0xFEFF:
		return true
	}
	switch {
	// Tab, newline and friends are the C0 controls a model can legally emit
	// inside a JSON string, and a vendor block printed on two lines arrives as
	// one. Left for the space folder below: deleting them glues the words
	// together ("Store\nInvoice" -> "StoreInvoice"). Every other C0 control,
	// NUL included, still goes.
	case r == '\t', r == '\n', r == '\v', r == '\f', r == '\r':
		return false
	case r < 0x20, r == 0x7F, r >= 0x80 && r <= 0x9F:
		return true
	case r >= 0x200B && r <= 0x200F:
		return true
	case r >= 0x202A && r <= 0x202E:
		return true
	case r >= 0x2066 && r <= 0x2069:
		return true
	}
	// Catches the remaining Cf/Cs/Co/Cn runes. Line and paragraph separators are
	// left alone here so the space folding can turn them into a plain space.
	return !unicode.IsGraphic(r) && !unicode.IsSpace(r)
}

// mapSeparators turns path separators and the Windows drive colon into '-' and
// deletes the characters Windows forbids outright.
func mapSeparators(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '/', '\\', ':':
			b.WriteRune('-')
		case '<', '>', '"', '|', '?', '*':
			continue
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// collapseDots reduces runs of two or more dots to one, which defeats "..".
func collapseDots(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	prevDot := false
	for _, r := range s {
		if r == '.' {
			if prevDot {
				continue
			}
			prevDot = true
		} else {
			prevDot = false
		}
		b.WriteRune(r)
	}
	return b.String()
}

// normalizeSpaces folds every Unicode space to ASCII 0x20 and collapses runs.
func normalizeSpaces(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	prevSpace := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			if prevSpace {
				continue
			}
			prevSpace = true
			b.WriteRune(' ')
			continue
		}
		prevSpace = false
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

// SanitizeFilename builds a final base name from an already-formatted stem.
func SanitizeFilename(stem, ext string) string {
	stem = SanitizeComponent(stem)
	first, _, _ := strings.Cut(stem, ".")
	if winReserved[strings.ToLower(first)] {
		stem = "_" + stem
	}
	ext = strings.ToLower(ext)
	if len(stem)+len(ext) > maxBaseLen {
		limit := maxBaseLen - len(ext)
		if limit < 0 {
			limit = 0
		}
		for limit > 0 && !utf8.RuneStart(stem[limit]) {
			limit--
		}
		stem = strings.TrimRight(stem[:limit], trimCutset)
	}
	if stem == "" {
		stem = "Untitled"
	}
	return stem + ext
}

// IsSafeBase reports whether name is a bare, contained filename.
func IsSafeBase(name string) bool {
	return name != "" && name != "." && name != ".." &&
		!strings.ContainsAny(name, `/\`) &&
		filepath.Base(name) == name &&
		!strings.HasPrefix(name, ".")
}
