package main

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseCompletion(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		want       ReceiptInfo
		wantErr    bool
		errMessage string
	}{
		{
			name: "Basic Receipt",
			input: `Date: 01/15/2023
Total: $123.45
Vendor: Test Store
Category: Food & Drink`,
			want: ReceiptInfo{
				StartDate: mustParseDate("01/15/2023"),
				EndDate:   mustParseDate("01/15/2023"),
				Total:     123.45,
				Vendor:    "Test Store",
				Category:  "Food & Drink",
			},
		},
		{
			name: "Hotel Receipt",
			input: `Check-in Date: 01/15/2023
Check-out Date: 01/17/2023
Total: $500.00
Vendor: Test Hotel
Category: Lodging`,
			want: ReceiptInfo{
				StartDate: mustParseDate("01/15/2023"),
				EndDate:   mustParseDate("01/17/2023"),
				Total:     500.00,
				Vendor:    "Test Hotel",
				Category:  "Lodging",
			},
		},
		{
			name: "Hotel Receipt with Missing Check-out",
			input: `Check-in Date: 01/15/2023
Total: $500.00
Vendor: Test Hotel
Category: Lodging`,
			want: ReceiptInfo{
				StartDate: mustParseDate("01/15/2023"),
				EndDate:   mustParseDate("01/15/2023"),
				Total:     500.00,
				Vendor:    "Test Hotel",
				Category:  "Lodging",
			},
		},
		{
			name: "Invalid Date Format",
			input: `Date: 2023-01-15
Total: $123.45
Vendor: Test Store
Category: Food`,
			wantErr:    true,
			errMessage: "invalid date format",
		},
		{
			name: "Invalid Total Format",
			input: `Date: 01/15/2023
Total: invalid
Vendor: Test Store
Category: Food`,
			wantErr:    true,
			errMessage: "invalid total format",
		},
		{
			name: "Receipt with Comma in Total",
			input: `Date: 01/15/2023
Total: $17,830.81
Vendor: Test Store
Category: Food & Drink`,
			want: ReceiptInfo{
				StartDate: mustParseDate("01/15/2023"),
				EndDate:   mustParseDate("01/15/2023"),
				Total:     17830.81,
				Vendor:    "Test Store",
				Category:  "Food & Drink",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseCompletion(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Error("parseCompletion() error = nil, wantErr true")
					return
				}
				if !strings.Contains(err.Error(), tt.errMessage) {
					t.Errorf("parseCompletion() error = %v, want error containing %v", err, tt.errMessage)
				}
				return
			}
			if err != nil {
				t.Errorf("parseCompletion() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseCompletion() = %v, want %v", got, tt.want)
			}
		})
	}
}

// Helper function to parse dates in tests
func mustParseDate(date string) time.Time {
	t, err := time.Parse("01/02/2006", date)
	if err != nil {
		panic(err)
	}
	return t
}

// Helper function to compare dates, handling zero values
func datesEqual(date1, date2 time.Time) bool {
	if date1.IsZero() && date2.IsZero() {
		return true
	}
	return date1.Equal(date2)
}

func TestGenerateFilename(t *testing.T) {
	tests := []struct {
		name     string
		info     ReceiptInfo
		expected string
	}{
		{
			name: "Regular Receipt",
			info: ReceiptInfo{
				Date:      mustParseDate("01/15/2023"),
				StartDate: mustParseDate("01/15/2023"),
				EndDate:   mustParseDate("01/15/2023"),
				Total:     123.45,
				Vendor:    "Test Store",
				Category:  "Food",
			},
			expected: "01-15-2023 - 123.45 - Test_Store - Food.pdf",
		},
		{
			name: "Hotel Receipt",
			info: ReceiptInfo{
				Date:      mustParseDate("01/15/2023"),
				StartDate: mustParseDate("01/15/2023"),
				EndDate:   mustParseDate("01/18/2023"),
				Total:     456.78,
				Vendor:    "Grand Hotel",
				Category:  "Lodging",
			},
			expected: "01-15-2023 to 01-18-2023 - 456.78 - Grand_Hotel - Lodging.pdf",
		},
		{
			name: "Receipt with Zero Total",
			info: ReceiptInfo{
				Date:      mustParseDate("01/15/2023"),
				StartDate: mustParseDate("01/15/2023"),
				EndDate:   mustParseDate("01/15/2023"),
				Total:     0.00,
				Vendor:    "Test Store",
				Category:  "Food",
			},
			expected: "01-15-2023 - 0.00 - Test_Store - Food.pdf",
		},
		{
			name: "Receipt with Large Total",
			info: ReceiptInfo{
				Date:      mustParseDate("01/15/2023"),
				StartDate: mustParseDate("01/15/2023"),
				EndDate:   mustParseDate("01/15/2023"),
				Total:     12345.67,
				Vendor:    "Test Store",
				Category:  "Food",
			},
			expected: "01-15-2023 - 12345.67 - Test_Store - Food.pdf",
		},
		{
			name: "Receipt with Special Characters",
			info: ReceiptInfo{
				Date:      mustParseDate("01/15/2023"),
				StartDate: mustParseDate("01/15/2023"),
				EndDate:   mustParseDate("01/15/2023"),
				Total:     123.45,
				Vendor:    "Test & Store",
				Category:  "Food & Drink",
			},
			expected: "01-15-2023 - 123.45 - Test_&_Store - Food_&_Drink.pdf",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := generateFilename(tt.info)
			if result != tt.expected {
				t.Errorf("Filename mismatch:\ngot  %v\nwant %v", result, tt.expected)
			}
		})
	}
}
