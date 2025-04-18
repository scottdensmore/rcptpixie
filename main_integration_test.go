package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dslipak/pdf"
	"github.com/jung-kurt/gofpdf"
)

func checkOllamaAvailable() bool {
	client := http.Client{
		Timeout: 2 * time.Second,
	}
	resp, err := client.Get("http://localhost:11434/api/tags")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func checkModelAvailable(model string) bool {
	client := http.Client{
		Timeout: 2 * time.Second,
	}
	resp, err := client.Get("http://localhost:11434/api/tags")
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	var tags struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return false
	}

	modelName := strings.Split(model, ":")[0]
	for _, m := range tags.Models {
		if strings.HasPrefix(m.Name, modelName) {
			return true
		}
	}
	return false
}

func createTestPDF(content string, outputPath string) error {
	pdf := gofpdf.New("P", "mm", "A4", "")
	pdf.AddPage()
	pdf.SetFont("Arial", "", 12)

	// Write each line of content
	for _, line := range strings.Split(content, "\n") {
		pdf.MultiCell(0, 10, line, "", "", false)
	}

	// Save the PDF
	return pdf.OutputFileAndClose(outputPath)
}

func TestPDFProcessing(t *testing.T) {
	// Skip if running in CI or if integration tests are disabled
	if os.Getenv("SKIP_INTEGRATION_TESTS") != "" {
		t.Skip("Skipping integration tests")
	}

	// Check if Ollama is available
	if !checkOllamaAvailable() {
		t.Skip("Ollama is not available")
	}

	// Create test PDFs
	regularContent := `Test Store
123 Main St
Anytown, USA

Date: 01/15/2023
Total: $123.45

Food
Tax: $10.00
Total: $123.45

Thank you for shopping at Test Store!`

	hotelContent := `Test Hotel
456 Hotel Ave
Anytown, USA

Check-in Date: 01/15/2023
Check-out Date: 01/17/2023
Total: $500.00

Lodging
Room: $450.00
Tax: $50.00
Total: $500.00

Thank you for staying at Test Hotel!`

	tests := []struct {
		name          string
		inputFileName string
		expectedName  string
		modelName     string
		expectedError bool
		errorContains string
		skipIfNoModel bool
		content       string
	}{
		{
			name:          "Regular Receipt",
			inputFileName: "regular_receipt.pdf",
			expectedName:  "01-15-2023 - 123.45 - Test_Store - Food.pdf",
			modelName:     "llama2:latest",
			skipIfNoModel: true,
			content:       regularContent,
		},
		{
			name:          "Hotel Receipt",
			inputFileName: "hotel_receipt.pdf",
			expectedName:  "01-15-2023 to 01-17-2023 - 500.00 - Test_Hotel - Lodging.pdf",
			modelName:     "llama2:latest",
			skipIfNoModel: true,
			content:       hotelContent,
		},
		{
			name:          "Invalid Model",
			inputFileName: "regular_receipt.pdf",
			modelName:     "nonexistent-model",
			expectedError: true,
			errorContains: "model 'nonexistent-model' not found",
			content:       regularContent,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Skip if model is required but not available
			if tt.skipIfNoModel && !checkModelAvailable(tt.modelName) {
				t.Skipf("Model %s not available", tt.modelName)
			}

			// Create a temporary directory for this test
			tempDir, err := os.MkdirTemp("", "rcptpixie-test-*")
			if err != nil {
				t.Fatalf("Failed to create temp directory: %v", err)
			}
			defer os.RemoveAll(tempDir)

			// Create the test PDF
			inputPath := filepath.Join(tempDir, tt.inputFileName)
			if err := createTestPDF(tt.content, inputPath); err != nil {
				t.Fatalf("Failed to create test PDF: %v", err)
			}

			// Verify the PDF can be read
			if _, err := pdf.Open(inputPath); err != nil {
				t.Fatalf("Failed to open test PDF: %v", err)
			}

			// Process the file
			err = processFile(inputPath, tt.modelName)

			// Check error cases
			if tt.expectedError {
				if err == nil {
					t.Error("Expected error but got none")
					return
				}
				if tt.errorContains != "" && !strings.Contains(err.Error(), tt.errorContains) {
					t.Errorf("Expected error containing %q but got %q", tt.errorContains, err.Error())
				}
				return
			}

			// Check success cases
			if err != nil {
				t.Fatalf("Failed to process file: %v", err)
			}

			// Check if the file was renamed correctly
			expectedPath := filepath.Join(tempDir, tt.expectedName)
			if _, err := os.Stat(expectedPath); os.IsNotExist(err) {
				t.Errorf("Expected renamed file %s does not exist", expectedPath)
			}
		})
	}
}
