package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestPDFProcessing(t *testing.T) {
	// Skip if running in CI or if integration tests are disabled
	if os.Getenv("SKIP_INTEGRATION_TESTS") != "" {
		t.Skip("Skipping integration tests")
	}

	// Check if Ollama is available
	if !checkOllamaAvailable() {
		t.Skip("Ollama is not available")
	}

	// Create a temporary directory for test files
	tempDir, err := os.MkdirTemp("", "rcptpixie-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	tests := []struct {
		name          string
		inputFileName string
		expectedName  string
		modelName     string
		expectedError bool
		errorContains string
		skipIfNoModel bool
	}{
		{
			name:          "Regular Receipt",
			inputFileName: "regular_receipt.pdf",
			expectedName:  "01-15-2023 - 123.45 - Test_Store - Food.pdf",
			modelName:     "llama2:latest",
			skipIfNoModel: true,
		},
		{
			name:          "Hotel Receipt",
			inputFileName: "hotel_receipt.pdf",
			expectedName:  "01-15-2023 to 01-17-2023 - 500.00 - Test_Hotel - Lodging.pdf",
			modelName:     "llama2:latest",
			skipIfNoModel: true,
		},
		{
			name:          "Invalid Model",
			inputFileName: "regular_receipt.pdf",
			modelName:     "nonexistent-model",
			expectedError: true,
			errorContains: "model 'nonexistent-model' not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Skip if model is required but not available
			if tt.skipIfNoModel && !checkModelAvailable(tt.modelName) {
				t.Skipf("Model %s not available", tt.modelName)
			}

			// Copy test PDF to temp directory
			inputPath := filepath.Join("testdata", tt.inputFileName)
			tempPath := filepath.Join(tempDir, tt.inputFileName)

			// Skip if test file doesn't exist (allows partial testing)
			if _, err := os.Stat(inputPath); os.IsNotExist(err) {
				t.Skipf("Test file %s not found", inputPath)
			}

			input, err := os.ReadFile(inputPath)
			if err != nil {
				t.Fatalf("Failed to read test file: %v", err)
			}

			err = os.WriteFile(tempPath, input, 0644)
			if err != nil {
				t.Fatalf("Failed to write temp file: %v", err)
			}

			// Process the file
			err = processFile(tempPath, tt.modelName)

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
