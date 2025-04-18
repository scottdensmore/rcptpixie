package rcptpixie

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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

func TestPDFProcessing(t *testing.T) {
	if !IsOllamaRunning() {
		t.Skip("Ollama service is not running")
	}

	if !IsModelAvailable("llama2") {
		t.Skip("llama2 model is not available")
	}

	// Create test data directory if it doesn't exist
	if err := os.MkdirAll("testdata", 0755); err != nil {
		t.Fatalf("Failed to create testdata directory: %v", err)
	}

	// Create test PDFs with various international formats
	testFiles := []struct {
		name    string
		content string
	}{
		{
			name: "regular_receipt.pdf",
			content: `Regular Receipt
Date: 2023-01-15
Total: $123.45
Vendor: Test Store
Category: Food & Drink`,
		},
		{
			name: "hotel_receipt.pdf",
			content: `Hotel Receipt
Start Date: 2023-01-10
End Date: 2023-01-15
Total: $1,234.56
Vendor: Grand Hotel
Category: Travel`,
		},
		{
			name: "european_receipt.pdf",
			content: `European Receipt
Date: 2023-01-15
Total: EUR 123.45
Vendor: European Store
Category: Shopping`,
		},
		{
			name: "uk_receipt.pdf",
			content: `UK Receipt
Date: 2023-01-15
Total: GBP 123.45
Vendor: UK Store
Category: Retail`,
		},
		{
			name: "japanese_receipt.pdf",
			content: `Japanese Receipt
Date: 2023-01-15
Total: JPY 12345
Vendor: Japanese Store
Category: Food`,
		},
		{
			name: "indian_receipt.pdf",
			content: `Indian Receipt
Date: 2023-01-15
Total: INR 12345.67
Vendor: Indian Store
Category: Grocery`,
		},
	}

	// Create all test PDFs
	for _, tf := range testFiles {
		filePath := filepath.Join("testdata", tf.name)
		if err := createTestPDF(filePath, tf.content); err != nil {
			t.Fatalf("Failed to create %s: %v", tf.name, err)
		}
	}

	// Test cases
	tests := []struct {
		name          string
		filename      string
		expectedError bool
	}{
		{
			name:          "Regular Receipt",
			filename:      filepath.Join("testdata", "regular_receipt.pdf"),
			expectedError: false,
		},
		{
			name:          "Hotel Receipt",
			filename:      filepath.Join("testdata", "hotel_receipt.pdf"),
			expectedError: false,
		},
		{
			name:          "European Receipt",
			filename:      filepath.Join("testdata", "european_receipt.pdf"),
			expectedError: false,
		},
		{
			name:          "UK Receipt",
			filename:      filepath.Join("testdata", "uk_receipt.pdf"),
			expectedError: false,
		},
		{
			name:          "Japanese Receipt",
			filename:      filepath.Join("testdata", "japanese_receipt.pdf"),
			expectedError: false,
		},
		{
			name:          "Indian Receipt",
			filename:      filepath.Join("testdata", "indian_receipt.pdf"),
			expectedError: false,
		},
		{
			name:          "Nonexistent File",
			filename:      filepath.Join("testdata", "nonexistent.pdf"),
			expectedError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ProcessFile(tt.filename, "llama2")
			if tt.expectedError {
				if err == nil {
					t.Error("Expected an error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
			}
		})
	}

	// Clean up test files
	for _, tf := range testFiles {
		os.Remove(filepath.Join("testdata", tf.name))
	}
}
