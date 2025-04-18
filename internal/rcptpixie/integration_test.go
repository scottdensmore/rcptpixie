package rcptpixie

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jung-kurt/gofpdf"
)

func createTestPDF(filename, content string) error {
	pdf := gofpdf.New("P", "mm", "A4", "")
	pdf.AddPage()
	pdf.SetFont("Arial", "", 12)

	// Write each line of content
	for _, line := range strings.Split(content, "\n") {
		pdf.MultiCell(0, 10, line, "", "", false)
	}

	// Save the PDF
	return pdf.OutputFileAndClose(filename)
}

func mustParseDate(dateStr string) time.Time {
	t, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		panic(fmt.Sprintf("invalid date format: %v", err))
	}
	return t
}

func TestPDFProcessing(t *testing.T) {
	if !IsOllamaRunning() {
		t.Skip("Ollama service is not running")
	}

	if !IsModelAvailable("llama2") {
		t.Skip("llama2 model is not available")
	}

	// Create test PDFs
	testFiles := []struct {
		name     string
		content  string
		expected ReceiptInfo
	}{
		{
			name: "Regular Receipt",
			content: `Receipt
Date: 2023-01-15
Total: 123.45
Vendor: Test Store
Category: Food`,
			expected: ReceiptInfo{
				StartDate: mustParseDate("2023-01-15"),
				EndDate:   mustParseDate("2023-01-15"),
				Total:     123.45,
				Vendor:    "Test Store",
				Category:  "Food",
			},
		},
		{
			name: "Hotel Receipt",
			content: `Hotel Receipt
Start Date: 2023-01-10
End Date: 2023-01-15
Total: 500.00
Vendor: Test Hotel
Category: Lodging`,
			expected: ReceiptInfo{
				StartDate: mustParseDate("2023-01-10"),
				EndDate:   mustParseDate("2023-01-15"),
				Total:     500.00,
				Vendor:    "Test Hotel",
				Category:  "Lodging",
			},
		},
		{
			name: "Restaurant Receipt",
			content: `Restaurant Receipt
Date: 2023-01-15
Total: 123.45
Vendor: Restaurant
Category: Food`,
			expected: ReceiptInfo{
				StartDate: mustParseDate("2023-01-15"),
				EndDate:   mustParseDate("2023-01-15"),
				Total:     123.45,
				Vendor:    "Restaurant",
				Category:  "Food",
			},
		},
		{
			name: "Store Receipt",
			content: `Store Receipt
Date: 2023-01-15
Total: 90.00
Vendor: Store
Category: Shopping`,
			expected: ReceiptInfo{
				StartDate: mustParseDate("2023-01-15"),
				EndDate:   mustParseDate("2023-01-15"),
				Total:     90.00,
				Vendor:    "Store",
				Category:  "Shopping",
			},
		},
		{
			name: "European Store Receipt",
			content: `European Store Receipt
Date: 2023-01-15
Total: 123.45
Vendor: European Store
Category: Shopping`,
			expected: ReceiptInfo{
				StartDate: mustParseDate("2023-01-15"),
				EndDate:   mustParseDate("2023-01-15"),
				Total:     123.45,
				Vendor:    "European Store",
				Category:  "Shopping",
			},
		},
		{
			name: "Multi-Category Receipt",
			content: `Multi-Category Receipt
Date: 2023-01-15
Total: 123.45
Vendor: Test Store
Category: Entertainment`,
			expected: ReceiptInfo{
				StartDate: mustParseDate("2023-01-15"),
				EndDate:   mustParseDate("2023-01-15"),
				Total:     123.45,
				Vendor:    "Test Store",
				Category:  "Entertainment",
			},
		},
		{
			name: "Invalid Receipt",
			content: `Invalid Receipt
Date: 2023-01-15
Total: invalid
Vendor: Test Store
Category: Food`,
			expected: ReceiptInfo{},
		},
	}

	// Create test directory if it doesn't exist
	testDir := "testdata"
	if err := os.MkdirAll(testDir, 0755); err != nil {
		t.Fatalf("Failed to create testdata directory: %v", err)
	}

	// Create test files
	for _, test := range testFiles {
		filename := filepath.Join(testDir, fmt.Sprintf("%s.pdf", test.name))
		if err := createTestPDF(filename, test.content); err != nil {
			t.Fatalf("Failed to create test PDF %s: %v", filename, err)
		}
	}

	// Run tests
	for _, test := range testFiles {
		t.Run(test.name, func(t *testing.T) {
			filename := filepath.Join(testDir, fmt.Sprintf("%s.pdf", test.name))
			err := ProcessFile(filename, "llama2")

			if test.name == "Invalid Receipt" {
				// Expect error for invalid receipt
				if err == nil {
					t.Error("Expected error for invalid receipt but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("ProcessFile() error = %v", err)
				return
			}

			// Verify the file was renamed correctly
			expectedFilename := filepath.Join(testDir, GenerateFilename(test.expected))
			if _, err := os.Stat(expectedFilename); os.IsNotExist(err) {
				t.Errorf("Expected file %s does not exist", expectedFilename)
			}
		})
	}

	// Test nonexistent file
	t.Run("Nonexistent File", func(t *testing.T) {
		err := ProcessFile("nonexistent.pdf", "llama2")
		if err == nil {
			t.Error("Expected error for nonexistent file")
		}
	})

	// Cleanup
	for _, test := range testFiles {
		filename := filepath.Join(testDir, fmt.Sprintf("%s.pdf", test.name))
		os.Remove(filename)
		if test.name != "Invalid Receipt" {
			expectedFilename := filepath.Join(testDir, GenerateFilename(test.expected))
			os.Remove(expectedFilename)
		}
	}
}
