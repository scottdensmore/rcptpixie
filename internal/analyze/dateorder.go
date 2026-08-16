package analyze

import (
	"strconv"
	"strings"
	"time"
)

// DateOrder is how a document writes a numeric date.
type DateOrder int

const (
	OrderUnknown DateOrder = iota
	OrderDayFirst
	OrderMonthFirst
)

// ParseDateOrder reads the -date-order flag. Anything unrecognised, including
// "auto", leaves the decision to the document.
func ParseDateOrder(s string) DateOrder { return parseOrder(s) }

func parseOrder(s string) DateOrder {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "day-first", "day_first", "dayfirst", "dmy":
		return OrderDayFirst
	case "month-first", "month_first", "monthfirst", "mdy":
		return OrderMonthFirst
	}
	return OrderUnknown
}

// numericDate is a date printed as three numbers, before anyone has decided
// which of the first two is the month.
type numericDate struct {
	a, b, year int // a and b are the first two fields as printed
}

// splitNumericDate reads "06/03/2025", "15.03.2025" or "11-02-23". It returns
// false for anything else — a spelled-out month is unambiguous and needs no
// help, and a year-first date has nothing to resolve.
func splitNumericDate(s string) (numericDate, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return numericDate{}, false
	}
	sep := strings.IndexAny(s, "/.-")
	if sep < 0 {
		return numericDate{}, false
	}
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == '/' || r == '.' || r == '-' })
	if len(parts) != 3 {
		return numericDate{}, false
	}
	n := make([]int, 3)
	for i, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" || len(p) > 4 {
			return numericDate{}, false
		}
		v, err := strconv.Atoi(p)
		if err != nil {
			return numericDate{}, false
		}
		n[i] = v
	}
	// A four-digit leading field is a year: 2025-06-03 is already unambiguous.
	if len(parts[0]) == 4 {
		return numericDate{}, false
	}
	year := n[2]
	if len(parts[2]) <= 2 {
		// Two-digit years: a receipt is not from the 1930s.
		year += 2000
		if year > time.Now().Year()+1 {
			year -= 100
		}
	}
	return numericDate{a: n[0], b: n[1], year: year}, true
}

// ambiguous reports whether the two leading fields could each be the month, so
// that reading them the other way round gives a different, equally valid date.
// 04/22/2024 is not ambiguous because 22 is no month; 05.05.2023 is not because
// swapping changes nothing.
func (d numericDate) ambiguous() bool {
	return d.a >= 1 && d.a <= 12 && d.b >= 1 && d.b <= 12 && d.a != d.b
}

func (d numericDate) resolve(order DateOrder) (time.Time, bool) {
	var month, day int
	switch order {
	case OrderDayFirst:
		day, month = d.a, d.b
	case OrderMonthFirst:
		month, day = d.a, d.b
	default:
		return time.Time{}, false
	}
	if month < 1 || month > 12 || day < 1 || day > 31 {
		return time.Time{}, false
	}
	t := time.Date(d.year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	// Reject a day the month does not have; time.Date would roll it forward.
	if int(t.Month()) != month || t.Day() != day {
		return time.Time{}, false
	}
	return t, true
}

// resolveAmbiguousDate re-reads the printed date when the document says which
// way round it is written and the digits alone cannot say. It returns the
// original when the printed form is absent, unambiguous, or not numeric, so the
// model's own answer stands unless there is a reason to overrule it.
func resolveAmbiguousDate(model time.Time, raw string, order DateOrder) (time.Time, bool) {
	if order == OrderUnknown || raw == "" {
		return model, false
	}
	nd, ok := splitNumericDate(raw)
	if !ok || !nd.ambiguous() {
		return model, false
	}
	fixed, ok := nd.resolve(order)
	if !ok || !plausibleDate(fixed) {
		return model, false
	}
	if fixed.Equal(model) {
		return model, false
	}
	return fixed, true
}

// textualDateLayouts are the printed forms a receipt uses when it does not use
// digits alone. None of them is ambiguous, so no convention is needed.
var textualDateLayouts = []string{
	"January 2, 2006", "Jan 2, 2006", "January 2 2006", "Jan 2 2006",
	"2 January 2006", "2 Jan 2006", "2006-01-02", "2006/01/02",
}

// dateFromRaw reads the date exactly as the document printed it. It is the
// fallback for a model that copies the receipt correctly and then scrambles its
// own ISO rendering of it — observed on a folio that reported date_raw
// "06.08.2023" alongside a date of "0682-20-23", which the schema's pattern
// happily allows.
func dateFromRaw(raw string, order DateOrder) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	for _, layout := range textualDateLayouts {
		if t, err := time.Parse(layout, raw); err == nil && plausibleDate(t) {
			return t, true
		}
	}
	nd, ok := splitNumericDate(raw)
	if !ok {
		return time.Time{}, false
	}
	if !nd.ambiguous() {
		// One field is too large to be a month, so the layout reads itself.
		inferred := OrderMonthFirst
		if nd.a > 12 {
			inferred = OrderDayFirst
		}
		if t, ok := nd.resolve(inferred); ok && plausibleDate(t) {
			return t, true
		}
		return time.Time{}, false
	}
	if t, ok := nd.resolve(order); ok && plausibleDate(t) {
		return t, true
	}
	return time.Time{}, false
}
