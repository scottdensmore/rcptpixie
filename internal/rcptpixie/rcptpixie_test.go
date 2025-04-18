package rcptpixie_test

import (
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
		name        string
		completion  string
		expected    rcptpixie.ReceiptInfo
		expectError bool
	}{
		{
			name: "Basic Receipt",
			completion: `Date: 2023-01-15
Total: 123.45
Vendor: Test Store
Category: Food`,
			expected: rcptpixie.ReceiptInfo{
				StartDate: mustParseDate("2023-01-15"),
				EndDate:   mustParseDate("2023-01-15"),
				Total:     123.45,
				Vendor:    "Test Store",
				Category:  "Food",
			},
			expectError: false,
		},
		{
			name: "Hotel Receipt",
			completion: `Start Date: 2023-01-10
End Date: 2023-01-15
Total: 1234.56
Vendor: Grand Hotel
Category: Lodging`,
			expected: rcptpixie.ReceiptInfo{
				StartDate: mustParseDate("2023-01-10"),
				EndDate:   mustParseDate("2023-01-15"),
				Total:     1234.56,
				Vendor:    "Grand Hotel",
				Category:  "Lodging",
			},
			expectError: false,
		},
		{
			name: "Invalid Date Format",
			completion: `Date: 2023/01/15
Total: 123.45
Vendor: Test Store
Category: Food & Drink`,
			expectError: true,
		},
		{
			name: "Missing Required Fields",
			completion: `Date: 2023-01-15
Vendor: Test Store`,
			expectError: true,
		},
		{
			name: "Invalid Total Format",
			completion: `Date: 2023-01-15
Total: abc
Vendor: Test Store
Category: Food & Drink`,
			expectError: true,
		},
		{
			name: "Multiple Categories",
			completion: `Date: 2023-01-15
Total: 123.45
Vendor: Test Store
Category: Entertainment`,
			expected: rcptpixie.ReceiptInfo{
				StartDate: mustParseDate("2023-01-15"),
				EndDate:   mustParseDate("2023-01-15"),
				Total:     123.45,
				Vendor:    "Test Store",
				Category:  "Entertainment",
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := rcptpixie.ParseCompletion(tt.completion)
			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got none")
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if !info.StartDate.Equal(tt.expected.StartDate) {
				t.Errorf("StartDate mismatch: got %v, want %v", info.StartDate, tt.expected.StartDate)
			}
			if !info.EndDate.Equal(tt.expected.EndDate) {
				t.Errorf("EndDate mismatch: got %v, want %v", info.EndDate, tt.expected.EndDate)
			}
			if info.Total != tt.expected.Total {
				t.Errorf("Total mismatch: got %v, want %v", info.Total, tt.expected.Total)
			}
			if info.Vendor != tt.expected.Vendor {
				t.Errorf("Vendor mismatch: got %v, want %v", info.Vendor, tt.expected.Vendor)
			}
			if info.Category != tt.expected.Category {
				t.Errorf("Category mismatch: got %v, want %v", info.Category, tt.expected.Category)
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
