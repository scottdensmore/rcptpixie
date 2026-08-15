package rename

import (
	"math/rand"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

func TestSanitizeComponentAttacks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"path traversal", "../../etc/passwd", "etc-passwd"},
		{"deep path traversal", "../../../etc/passwd", "etc-passwd"},
		{"absolute path", "/etc/shadow", "etc-shadow"},
		{"bare dotdot", "..", "Untitled"},
		{"single dot", ".", "Untitled"},
		{"vendor with slash", "AT&T / Wireless", "AT&T - Wireless"},
		{"category with slash", "Groceries/Food", "Groceries-Food"},
		// The colon and the backslash each map to '-', so adjacent separators
		// leave a doubled dash. Ugly, harmless, and not collapsed on purpose:
		// dash runs carry meaning in the receipt name format.
		{"windows path", `C:\Windows\cmd`, "C--Windows-cmd"},
		{"backslash traversal", `..\..\windows\system32`, "windows-system32"},
		{"drive colon", "C:report", "C-report"},
		{"time colon", "Invoice 12:30", "Invoice 12-30"},
		{"rtl override", "Invoice\u202Egnp.exe", "Invoicegnp.exe"},
		{"rtl embedding", "\u202Bvendor\u202C", "vendor"},
		{"isolate", "a\u2066b\u2069c", "abc"},
		{"zero width", "Report\u200b\u200bDraft", "ReportDraft"},
		{"soft hyphen", "Re\u00adport", "Report"},
		{"word joiner and bom", "A\u2060B\ufeffC", "ABC"},
		// Whitespace controls fold to a space instead of vanishing: a two-line
		// vendor block must not come back as one fused word.
		{"newline becomes space", "line1\nline2", "line1 line2"},
		{"crlf becomes one space", "line1\r\nline2", "line1 line2"},
		{"tab becomes space", "a\tb", "a b"},
		{"multiline vendor", "Store\nInvoice", "Store Invoice"},
		{"newline run collapses", "Grand\n\n\nHotel", "Grand Hotel"},
		{"leading and trailing newlines", "\n Acme \t", "Acme"},
		{"vertical tab and form feed", "a\vb\fc", "a b c"},
		{"nul byte", "payload\x00.pdf", "payload.pdf"},
		{"escape sequence", "vendor\x1b[31m", "vendor[31m"},
		{"c1 control", "vendor\u0085name", "vendorname"},
		{"line separator becomes space", "a\u2028b", "a b"},
		{"nbsp becomes space", "a\u00a0b", "a b"},
		{"space run collapses", "Test    Store", "Test Store"},
		{"windows forbidden chars", `a<b>c"d|e?f*g`, "abcdefg"},
		{"leading dot", ".hidden", "hidden"},
		{"trailing dots", "trailing dots...", "trailing dots"},
		{"interior dots collapse", "a..b...c", "a.b.c"},
		{"leading and trailing junk", "__- .Store. -__", "Store"},
		{"only spaces", "   ", "Untitled"},
		{"only punctuation", "...---___", "Untitled"},
		{"empty", "", "Untitled"},
		{"all deleted", "<>?*|", "Untitled"},
		{"invalid utf8 dropped", "caf\xe9 bar", "caf bar"},
		{"lone continuation byte", "\x80\x80vendor", "vendor"},
		{"traversal after separators", "x/../../tmp/pwned", "x-.-.-tmp-pwned"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := SanitizeComponent(tt.in)
			if got != tt.want {
				t.Errorf("SanitizeComponent(%q) = %q, want %q", tt.in, got, tt.want)
			}
			if again := SanitizeComponent(got); again != got {
				t.Errorf("SanitizeComponent not idempotent: %q -> %q -> %q", tt.in, got, again)
			}
		})
	}
}

// TestSanitizeComponentPreserves guards the shipped receipt filenames: '&' and
// ',' are legal and the golden names contain them.
func TestSanitizeComponentPreserves(t *testing.T) {
	t.Parallel()

	for _, s := range []string{
		"Test & Store",
		"Food & Drink",
		"Food, Drink",
		"Test Store",
		"Test_Store",
		"123.45",
		"Café Ñoño",
		"Acme Inc. Supplies",
		"Hotel (Downtown) #12",
	} {
		t.Run(s, func(t *testing.T) {
			t.Parallel()
			if got := SanitizeComponent(s); got != s {
				t.Errorf("SanitizeComponent(%q) = %q, want unchanged", s, got)
			}
		})
	}
}

func TestSanitizeFilenameReservedNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		stem string
		ext  string
		want string
	}{
		{"CON", ".pdf", "_CON.pdf"},
		{"con", ".pdf", "_con.pdf"},
		{"Con", ".pdf", "_Con.pdf"},
		{"PRN", ".pdf", "_PRN.pdf"},
		{"AUX", ".pdf", "_AUX.pdf"},
		{"NUL", ".pdf", "_NUL.pdf"},
		{"COM1", ".pdf", "_COM1.pdf"},
		{"com9", ".pdf", "_com9.pdf"},
		{"LPT1", ".pdf", "_LPT1.pdf"},
		{"lpt9", ".pdf", "_lpt9.pdf"},
		{"nul.txt", ".pdf", "_nul.txt.pdf"},
		{"CON.backup.old", ".pdf", "_CON.backup.old.pdf"},
		{"COMET", ".pdf", "COMET.pdf"},
		{"CONTOSO", ".pdf", "CONTOSO.pdf"},
		{"COM0", ".pdf", "COM0.pdf"},
		{"COM10", ".pdf", "COM10.pdf"},
		{"LPT", ".pdf", "LPT.pdf"},
		{"Concord Hotel", ".pdf", "Concord Hotel.pdf"},
	}

	for _, tt := range tests {
		t.Run(tt.stem, func(t *testing.T) {
			t.Parallel()
			got := SanitizeFilename(tt.stem, tt.ext)
			if got != tt.want {
				t.Errorf("SanitizeFilename(%q, %q) = %q, want %q", tt.stem, tt.ext, got, tt.want)
			}
			if !IsSafeBase(got) {
				t.Errorf("SanitizeFilename(%q, %q) = %q, not a safe base", tt.stem, tt.ext, got)
			}
		})
	}
}

func TestSanitizeFilenameGeneral(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		stem string
		ext  string
		want string
	}{
		{"lowercases ext", "Receipt", ".PDF", "Receipt.pdf"},
		{"traversal stem", "../../../etc/passwd", ".pdf", "etc-passwd.pdf"},
		{"trailing dots and spaces", "report.  ", ".pdf", "report.pdf"},
		{"empty stem", "", ".pdf", "Untitled.pdf"},
		{"punctuation stem", "...", ".pdf", "Untitled.pdf"},
		{"no ext", "Receipt", "", "Receipt"},
		{"receipt golden", "01-15-2023 - 123.45 - Test_Store - Food", ".pdf", "01-15-2023 - 123.45 - Test_Store - Food.pdf"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := SanitizeFilename(tt.stem, tt.ext); got != tt.want {
				t.Errorf("SanitizeFilename(%q, %q) = %q, want %q", tt.stem, tt.ext, got, tt.want)
			}
		})
	}
}

func TestSanitizeFilenameLengthCap(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		stem string
		ext  string
	}{
		{"ascii", strings.Repeat("a", 400), ".pdf"},
		{"emoji", strings.Repeat("😀", 400), ".pdf"},
		{"cjk", strings.Repeat("漢", 400), ".pdf"},
		{"mixed", strings.Repeat("aé漢😀", 200), ".jpeg"},
		{"long ext", strings.Repeat("a", 400), strings.ToLower(strings.Repeat(".x", 60))},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := SanitizeFilename(tt.stem, tt.ext)
			if len(got) > maxBaseLen {
				t.Errorf("len(result) = %d, want <= %d", len(got), maxBaseLen)
			}
			if !utf8.ValidString(got) {
				t.Errorf("result is not valid UTF-8: %q", got)
			}
			if strings.ContainsRune(got, utf8.RuneError) {
				t.Errorf("result split a multibyte rune: %q", got)
			}
			if !IsSafeBase(got) {
				t.Errorf("result is not a safe base: %q", got)
			}
		})
	}
}

// TestSanitizeFilenameRuneBoundary walks a stem across the truncation point one
// rune at a time so the cap is exercised at every offset within a 4-byte rune.
func TestSanitizeFilenameRuneBoundary(t *testing.T) {
	t.Parallel()

	for pad := 0; pad < 8; pad++ {
		stem := strings.Repeat("a", pad) + strings.Repeat("😀", 100)
		got := SanitizeFilename(stem, ".pdf")
		if len(got) > maxBaseLen {
			t.Fatalf("pad=%d: len = %d, want <= %d", pad, len(got), maxBaseLen)
		}
		if !utf8.ValidString(got) || strings.ContainsRune(got, utf8.RuneError) {
			t.Fatalf("pad=%d: invalid UTF-8 result %q", pad, got)
		}
		if !strings.HasSuffix(got, ".pdf") {
			t.Fatalf("pad=%d: result %q lost its extension", pad, got)
		}
	}
}

func TestIsSafeBase(t *testing.T) {
	t.Parallel()

	unsafe := []string{
		"", ".", "..", "/", "a/b", `a\b`, "/etc/passwd", "../x",
		".hidden", "./x", `..\x`,
	}
	for _, s := range unsafe {
		if IsSafeBase(s) {
			t.Errorf("IsSafeBase(%q) = true, want false", s)
		}
	}

	safe := []string{"a", "Receipt.pdf", "01-15-2023 - 123.45 - Test_Store - Food.pdf", "_CON.pdf", "Café.pdf"}
	for _, s := range safe {
		if !IsSafeBase(s) {
			t.Errorf("IsSafeBase(%q) = false, want true", s)
		}
	}
}

// TestSanitizeProperties runs the invariants over a generated corpus that mixes
// attack fragments with random bytes, including invalid UTF-8 sequences.
func TestSanitizeProperties(t *testing.T) {
	t.Parallel()

	for i, in := range corpus(2000) {
		got := SanitizeComponent(in)

		if again := SanitizeComponent(got); again != got {
			t.Fatalf("corpus[%d]: not idempotent: %q -> %q -> %q", i, in, got, again)
		}
		if got == "" {
			t.Fatalf("corpus[%d]: empty result for %q", i, in)
		}
		if !utf8.ValidString(got) {
			t.Fatalf("corpus[%d]: invalid UTF-8 result %q for input %q", i, got, in)
		}
		if strings.ContainsAny(got, `/\:`) {
			t.Fatalf("corpus[%d]: separator survived: %q -> %q", i, in, got)
		}
		if strings.ContainsAny(got, `<>"|?*`) {
			t.Fatalf("corpus[%d]: forbidden char survived: %q -> %q", i, in, got)
		}
		if strings.Contains(got, "..") {
			t.Fatalf("corpus[%d]: dot run survived: %q -> %q", i, in, got)
		}
		for _, r := range got {
			if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
				t.Fatalf("corpus[%d]: control rune %U survived: %q -> %q", i, r, in, got)
			}
			if !unicode.IsGraphic(r) || unicode.In(r, unicode.Cf, unicode.Cs, unicode.Co) {
				t.Fatalf("corpus[%d]: invisible rune %U survived: %q -> %q", i, r, in, got)
			}
		}
		if strings.HasPrefix(got, " ") || strings.HasSuffix(got, " ") {
			t.Fatalf("corpus[%d]: untrimmed result %q", i, got)
		}

		name := SanitizeFilename(in, ".pdf")
		if !IsSafeBase(name) {
			t.Fatalf("corpus[%d]: SanitizeFilename(%q) = %q, not a safe base", i, in, name)
		}
		if len(name) > maxBaseLen {
			t.Fatalf("corpus[%d]: name %q is %d bytes", i, name, len(name))
		}
		if again := SanitizeFilename(name, ""); again != name {
			t.Fatalf("corpus[%d]: SanitizeFilename not idempotent: %q -> %q -> %q", i, in, name, again)
		}
	}
}

func corpus(n int) []string {
	fragments := []string{
		"..", "../", "/", `\`, ":", ".", "-", "_", " ", "\t", "\n", "\x00",
		"etc", "passwd", "Test Store", "Food & Drink", "Food, Drink", "CON", "NUL.txt",
		"\u202e", "\u200b", "\ufeff", "\u00ad", "\u2066", "\u0085", "\u00a0", "\u2028",
		"<", ">", `"`, "|", "?", "*", "😀", "漢", "é", "\x80", "\xff\xfe", "%s", "$(rm -rf /)",
		"", ".pdf", ".exe", "COM1", "lpt9", "AT&T", "Groceries/Food",
	}

	rng := rand.New(rand.NewSource(20240315))
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		var b strings.Builder
		for j := rng.Intn(10); j >= 0; j-- {
			switch rng.Intn(4) {
			case 0:
				b.WriteByte(byte(rng.Intn(256)))
			case 1:
				b.WriteRune(rune(rng.Intn(0x110000)))
			default:
				b.WriteString(fragments[rng.Intn(len(fragments))])
			}
		}
		out = append(out, b.String())
	}
	return out
}
