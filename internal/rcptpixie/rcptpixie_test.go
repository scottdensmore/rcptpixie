package rcptpixie_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/scottdensmore/rcptpixie/internal/rcptpixie"
)

func mustParseDate(dateStr string) time.Time {
	t, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		panic(err)
	}
	return t
}

func TestParseCompletion(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected rcptpixie.ReceiptInfo
		wantErr  bool
	}{
		{
			name: "Basic Receipt",
			input: `Date: 2023-01-15
Total: $123.45
Vendor: Test Store
Category: Food & Drink`,
			expected: rcptpixie.ReceiptInfo{
				StartDate: mustParseDate("2023-01-15"),
				EndDate:   mustParseDate("2023-01-15"),
				Total:     123.45,
				Vendor:    "Test Store",
				Category:  "Food & Drink",
			},
			wantErr: false,
		},
		{
			name: "Hotel Receipt",
			input: `Start Date: 2023-01-15
End Date: 2023-01-17
Total: $456.78
Vendor: Grand Hotel
Category: Travel`,
			expected: rcptpixie.ReceiptInfo{
				StartDate: mustParseDate("2023-01-15"),
				EndDate:   mustParseDate("2023-01-17"),
				Total:     456.78,
				Vendor:    "Grand Hotel",
				Category:  "Travel",
			},
			wantErr: false,
		},
		{
			name: "Invalid Date Format",
			input: `Date: 01/15/2023
Total: $123.45
Vendor: Test Store
Category: Food & Drink`,
			expected: rcptpixie.ReceiptInfo{},
			wantErr:  true,
		},
		{
			name: "Missing Required Fields",
			input: `Date: 2023-01-15
Vendor: Test Store`,
			expected: rcptpixie.ReceiptInfo{},
			wantErr:  true,
		},
		{
			name: "Invalid Total Format",
			input: `Date: 2023-01-15
Total: abc
Vendor: Test Store
Category: Food & Drink`,
			expected: rcptpixie.ReceiptInfo{},
			wantErr:  true,
		},
		{
			name: "Receipt with Comma in Total",
			input: `Date: 2023-01-15
Total: $17,830.81
Vendor: Test Store
Category: Food & Drink`,
			expected: rcptpixie.ReceiptInfo{
				StartDate: mustParseDate("2023-01-15"),
				EndDate:   mustParseDate("2023-01-15"),
				Total:     17830.81,
				Vendor:    "Test Store",
				Category:  "Food & Drink",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := rcptpixie.ParseCompletion(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseCompletion() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("ParseCompletion() = %v, want %v", got, tt.expected)
			}
		})
	}
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
		info     rcptpixie.ReceiptInfo
		expected string
	}{
		{
			name: "Regular Receipt",
			info: rcptpixie.ReceiptInfo{
				StartDate: mustParseDate("2023-01-15"),
				EndDate:   mustParseDate("2023-01-15"),
				Total:     123.45,
				Vendor:    "Test Store",
				Category:  "Food",
			},
			expected: "01-15-2023 - 123.45 - Test_Store - Food.pdf",
		},
		{
			name: "Hotel Receipt",
			info: rcptpixie.ReceiptInfo{
				StartDate: mustParseDate("2023-01-15"),
				EndDate:   mustParseDate("2023-01-18"),
				Total:     456.78,
				Vendor:    "Grand Hotel",
				Category:  "Lodging",
			},
			expected: "01-15-2023 to 01-18-2023 - 456.78 - Grand_Hotel - Lodging.pdf",
		},
		{
			name: "Receipt with Special Characters",
			info: rcptpixie.ReceiptInfo{
				StartDate: mustParseDate("2023-01-15"),
				EndDate:   mustParseDate("2023-01-15"),
				Total:     123.45,
				Vendor:    "Test & Store",
				Category:  "Food & Drink",
			},
			expected: "01-15-2023 - 123.45 - Test_&_Store - Food_&_Drink.pdf",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := rcptpixie.GenerateFilename(tt.info)
			if result != tt.expected {
				t.Errorf("Filename mismatch:\ngot  %v\nwant %v", result, tt.expected)
			}
		})
	}
}
