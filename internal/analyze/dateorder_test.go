package analyze

import (
	"testing"
	"time"
)

func d(y int, m time.Month, day int) time.Time {
	return time.Date(y, m, day, 0, 0, 0, 0, time.UTC)
}

func TestSplitNumericDate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in         string
		ok         bool
		a, b, year int
	}{
		{"06/03/2025", true, 6, 3, 2025},
		{"15.03.2025", true, 15, 3, 2025},
		{"11-02-2023", true, 11, 2, 2023},
		{" 1/8/2024 ", true, 1, 8, 2024},
		{"11/02/23", true, 11, 2, 2023},
		// A leading four-digit year is already unambiguous.
		{"2025-06-03", false, 0, 0, 0},
		// Nothing numeric to reorder.
		{"March 4, 2024", false, 0, 0, 0},
		{"4 Mar 2024", false, 0, 0, 0},
		{"", false, 0, 0, 0},
		{"06/2025", false, 0, 0, 0},
		{"06/03/2025/1", false, 0, 0, 0},
		{"aa/bb/cccc", false, 0, 0, 0},
	}
	for _, tc := range cases {
		got, ok := splitNumericDate(tc.in)
		if ok != tc.ok {
			t.Errorf("splitNumericDate(%q) ok = %v, want %v", tc.in, ok, tc.ok)
			continue
		}
		if !ok {
			continue
		}
		if got.a != tc.a || got.b != tc.b || got.year != tc.year {
			t.Errorf("splitNumericDate(%q) = %d,%d,%d want %d,%d,%d",
				tc.in, got.a, got.b, got.year, tc.a, tc.b, tc.year)
		}
	}
}

func TestAmbiguous(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   string
		want bool
	}{
		{"06/03/2025", true},  // either reading is a real date
		{"04/22/2024", false}, // 22 is no month
		{"22/04/2024", false},
		{"05.05.2023", false}, // swapping changes nothing
		{"13/01/2024", false},
		{"01/13/2024", false},
	}
	for _, tc := range cases {
		nd, ok := splitNumericDate(tc.in)
		if !ok {
			t.Fatalf("splitNumericDate(%q) failed", tc.in)
		}
		if got := nd.ambiguous(); got != tc.want {
			t.Errorf("%q ambiguous = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// The measured failure: the model reads 06/03/2025 as the sixth of March on a
// receipt from a US coffee shop. The document says which way round it is.
func TestResolveAmbiguousDate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		model   time.Time
		raw     string
		order   DateOrder
		want    time.Time
		changed bool
	}{
		{"swapped, document says month-first",
			d(2025, time.March, 6), "06/03/2025", OrderMonthFirst, d(2025, time.June, 3), true},
		{"swapped, document says day-first",
			d(2024, time.July, 4), "07/04/2024", OrderDayFirst, d(2024, time.April, 7), true},
		{"already right, no change",
			d(2025, time.June, 3), "06/03/2025", OrderMonthFirst, d(2025, time.June, 3), false},
		{"unknown order leaves the model's answer alone",
			d(2025, time.March, 6), "06/03/2025", OrderUnknown, d(2025, time.March, 6), false},
		{"unambiguous printed date is never overruled",
			d(2024, time.April, 22), "04/22/2024", OrderDayFirst, d(2024, time.April, 22), false},
		{"same day and month cannot be swapped",
			d(2023, time.May, 5), "05.05.2023", OrderDayFirst, d(2023, time.May, 5), false},
		{"spelled month is left alone",
			d(2024, time.March, 4), "March 4, 2024", OrderDayFirst, d(2024, time.March, 4), false},
		{"missing printed date leaves the model's answer alone",
			d(2025, time.March, 6), "", OrderMonthFirst, d(2025, time.March, 6), false},
		{"two-digit year still resolves",
			d(2023, time.February, 11), "11/02/23", OrderDayFirst, d(2023, time.February, 11), false},
		{"two-digit year, month-first reading",
			d(2023, time.February, 11), "11/02/23", OrderMonthFirst, d(2023, time.November, 2), true},
		{"implausible result is rejected",
			d(2025, time.March, 6), "06/03/1820", OrderMonthFirst, d(2025, time.March, 6), false},
		{"garbage printed date is ignored",
			d(2025, time.March, 6), "//", OrderMonthFirst, d(2025, time.March, 6), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, changed := resolveAmbiguousDate(tc.model, tc.raw, tc.order)
			if !got.Equal(tc.want) || changed != tc.changed {
				t.Errorf("resolveAmbiguousDate(%s, %q, %v) = %s,%v want %s,%v",
					tc.model.Format("2006-01-02"), tc.raw, tc.order,
					got.Format("2006-01-02"), changed, tc.want.Format("2006-01-02"), tc.changed)
			}
		})
	}
}

// A day the month does not have must be refused rather than rolled forward,
// which is what time.Date would do with 31 February.
func TestResolveRejectsImpossibleDay(t *testing.T) {
	t.Parallel()

	model := d(2024, time.February, 11)
	got, changed := resolveAmbiguousDate(model, "11/02/2024", OrderMonthFirst)
	if !got.Equal(d(2024, time.November, 2)) || !changed {
		t.Fatalf("got %s changed=%v", got.Format("2006-01-02"), changed)
	}

	// 02/30 is not a date in either reading, so nothing is overruled.
	if got, changed := resolveAmbiguousDate(model, "02/30/2024", OrderDayFirst); !got.Equal(model) || changed {
		t.Errorf("30 February was accepted: got %s changed=%v", got.Format("2006-01-02"), changed)
	}
}

func TestParseOrder(t *testing.T) {
	t.Parallel()

	for in, want := range map[string]DateOrder{
		"day-first": OrderDayFirst, "DAY-FIRST": OrderDayFirst, " day_first ": OrderDayFirst,
		"month-first": OrderMonthFirst, "MonthFirst": OrderMonthFirst,
		"unknown": OrderUnknown, "": OrderUnknown, "sideways": OrderUnknown,
	} {
		if got := parseOrder(in); got != want {
			t.Errorf("parseOrder(%q) = %v, want %v", in, got, want)
		}
	}
}

// The rescue path: a folio reported date_raw "06.08.2023" beside a date of
// "0682-20-23", which the schema's pattern allows. The copy is the better
// source, and losing the file over the model's own typo would be worse.
func TestDateFromRaw(t *testing.T) {
	t.Parallel()

	cases := []struct {
		raw   string
		order DateOrder
		want  time.Time
		ok    bool
	}{
		{"06.08.2023", OrderDayFirst, d(2023, time.August, 6), true},
		{"06.08.2023", OrderMonthFirst, d(2023, time.June, 8), true},
		// Unambiguous, so it reads itself with no convention needed.
		{"22/04/2024", OrderUnknown, d(2024, time.April, 22), true},
		{"04/22/2024", OrderUnknown, d(2024, time.April, 22), true},
		{"2024-07-14", OrderUnknown, d(2024, time.July, 14), true},
		{"March 4, 2024", OrderUnknown, d(2024, time.March, 4), true},
		{"4 Mar 2024", OrderUnknown, d(2024, time.March, 4), true},
		{"Jan 8, 2024", OrderUnknown, d(2024, time.January, 8), true},
		// Ambiguous with no convention: refuse rather than guess.
		{"06/03/2025", OrderUnknown, time.Time{}, false},
		{"", OrderUnknown, time.Time{}, false},
		{"not a date", OrderUnknown, time.Time{}, false},
		{"31/02/2024", OrderDayFirst, time.Time{}, false},
		{"06/03/1820", OrderMonthFirst, time.Time{}, false},
	}
	for _, tc := range cases {
		got, ok := dateFromRaw(tc.raw, tc.order)
		if ok != tc.ok {
			t.Errorf("dateFromRaw(%q, %v) ok = %v, want %v", tc.raw, tc.order, ok, tc.ok)
			continue
		}
		if ok && !got.Equal(tc.want) {
			t.Errorf("dateFromRaw(%q, %v) = %s, want %s", tc.raw, tc.order,
				got.Format("2006-01-02"), tc.want.Format("2006-01-02"))
		}
	}
}
