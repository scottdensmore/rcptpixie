package rcptpixie

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/dslipak/pdf"
)

// Logger is a custom logger that only prints when verbose mode is enabled
type Logger struct {
	verbose bool
}

func (l *Logger) Printf(format string, v ...interface{}) {
	if l.verbose {
		log.Printf(format, v...)
	}
}

var Log Logger

func InitLogger(verbose bool) {
	Log = Logger{verbose: verbose}
}

// ReceiptInfo represents the extracted information from a receipt
type ReceiptInfo struct {
	Date      time.Time
	Total     float64
	Vendor    string
	Category  string
	StartDate time.Time
	EndDate   time.Time
	StayDates string
	IsHotel   bool
}

// OllamaRequest represents a request to the Ollama API
type OllamaRequest struct {
	Model   string                 `json:"model"`
	Prompt  string                 `json:"prompt"`
	Options map[string]interface{} `json:"options"`
}

// OllamaResponse represents a response from the Ollama API
type OllamaResponse struct {
	Response string `json:"response"`
}

// ExtractTextFromPDF extracts text content from a PDF file
func ExtractTextFromPDF(filePath string) (string, error) {
	Log.Printf("Extracting text from PDF: %s", filePath)
	// Open the PDF file
	r, err := pdf.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("error opening PDF: %v", err)
	}

	var textBuilder strings.Builder
	// Extract text from all pages
	totalPages := r.NumPage()
	Log.Printf("PDF has %d pages", totalPages)

	for i := 1; i <= totalPages; i++ {
		Log.Printf("Processing page %d of %d", i, totalPages)
		p := r.Page(i)
		if p.V.IsNull() {
			Log.Printf("Page %d is null, skipping", i)
			continue
		}
		text, err := p.GetPlainText(nil)
		if err != nil {
			return "", fmt.Errorf("error extracting text from page %d: %v", i, err)
		}
		textBuilder.WriteString(text)
		textBuilder.WriteString("\n")
	}

	extractedText := textBuilder.String()
	Log.Printf("Successfully extracted %d characters of text", len(extractedText))
	return extractedText, nil
}

// ProcessFile processes a single PDF file
func ProcessFile(filePath string, modelName string) error {
	Log.Printf("Starting to process file: %s", filePath)

	// Verify file exists and is a PDF
	if !strings.HasSuffix(strings.ToLower(filePath), ".pdf") {
		return fmt.Errorf("file %s is not a PDF", filePath)
	}

	// Extract text from PDF
	Log.Printf("Extracting text from PDF")
	textContent, err := ExtractTextFromPDF(filePath)
	if err != nil {
		return fmt.Errorf("error extracting text from PDF: %v", err)
	}

	// Create prompt for the LLM
	Log.Printf("Creating prompt for LLM")
	prompt := fmt.Sprintf(`You are a helpful assistant that extracts information from receipts. Please analyze this existing receipt and extract the following information:

Receipt Text:
%s

Please extract and provide ONLY the following information in this exact format:
Date: YYYY-MM-DD (for regular receipts)
Start Date: YYYY-MM-DD (for hotel receipts)
End Date: YYYY-MM-DD (for hotel receipts)
Total: XXXX.XX (numeric value only, no currency symbols or text)
Vendor: Name
Category: Type (choose the single most appropriate category)

Important instructions:
1. Keep the original currency amount without conversion
2. Do not include any currency symbols, codes, or text
3. For hotel receipts, use Start Date and End Date instead of Date
4. If any field cannot be determined from the receipt, leave it empty but keep the label
5. For dates, always use YYYY-MM-DD format
6. For totals, always use decimal point (.) and no currency symbols or text
7. For category, choose ONE most appropriate category (do not list multiple categories)
8. Do not include any additional text or notes in the output

Example output for a regular receipt:
Date: 2023-01-15
Total: 123.45
Vendor: Test Store
Category: Food

Example output for a hotel receipt:
Start Date: 2023-01-10
End Date: 2023-01-15
Total: 1234.56
Vendor: Grand Hotel
Category: Lodging`, textContent)

	// Create request to Ollama
	Log.Printf("Creating request for Ollama model: %s", modelName)
	reqBody := OllamaRequest{
		Model:  modelName,
		Prompt: prompt,
		Options: map[string]interface{}{
			"temperature": 0.1,
		},
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("error marshaling request: %v", err)
	}

	// Send request to Ollama
	Log.Printf("Sending request to Ollama")
	resp, err := http.Post("http://localhost:11434/api/generate", "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("error connecting to Ollama: %v. Make sure Ollama is running and the model '%s' is available", err, modelName)
	}
	defer resp.Body.Close()

	// Check for model not found error
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("model '%s' not found. Please make sure the model is available in Ollama", modelName)
	}

	Log.Printf("Received response from Ollama with status: %d", resp.StatusCode)

	// Read response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("error reading response: %v", err)
	}

	var ollamaResp OllamaResponse
	if err := json.Unmarshal(body, &ollamaResp); err != nil {
		Log.Printf("Attempting to parse streaming response")
		// Try to parse the response as a streaming response
		lines := strings.Split(string(body), "\n")
		var fullResponse strings.Builder
		for _, line := range lines {
			if line == "" {
				continue
			}
			var streamResp struct {
				Response string `json:"response"`
				Done     bool   `json:"done"`
			}
			if err := json.Unmarshal([]byte(line), &streamResp); err != nil {
				continue
			}
			fullResponse.WriteString(streamResp.Response)
			if streamResp.Done {
				break
			}
		}
		ollamaResp.Response = fullResponse.String()
	}

	Log.Printf("Parsing Ollama response")
	// Parse the response and create ReceiptInfo
	info, err := ParseCompletion(ollamaResp.Response)
	if err != nil {
		return fmt.Errorf("error parsing completion: %v", err)
	}

	// Skip renaming if we don't have enough information
	if info.Date.IsZero() && info.Total == 0 && info.Vendor == "" && info.Category == "" {
		return fmt.Errorf("could not extract enough information from the receipt")
	}

	Log.Printf("Extracted Receipt Info: Date: %v, Total: %.2f, Vendor: %s, Category: %s",
		info.Date, info.Total, info.Vendor, info.Category)

	// Generate new filename
	dir := filepath.Dir(filePath)
	baseName := filepath.Base(filePath)
	ext := filepath.Ext(baseName)

	// Format dates for filename
	startDate := info.StartDate.Format("01-02-2006")
	endDate := info.EndDate.Format("01-02-2006")

	// Only include "to" if dates are different
	datePart := startDate
	if !info.StartDate.Equal(info.EndDate) {
		datePart = fmt.Sprintf("%s to %s", startDate, endDate)
	}

	// Replace spaces with underscores and handle commas in categories
	vendorPart := strings.ReplaceAll(info.Vendor, " ", "_")
	categoryPart := strings.ReplaceAll(info.Category, " ", "_")
	categoryPart = strings.ReplaceAll(categoryPart, ",", ",_")

	newName := fmt.Sprintf("%s - %.2f - %s - %s%s",
		datePart,
		info.Total,
		vendorPart,
		categoryPart,
		ext)

	Log.Printf("Generated new filename: %s", newName)

	// Rename the file
	newPath := filepath.Join(dir, newName)
	if err := os.Rename(filePath, newPath); err != nil {
		return fmt.Errorf("error renaming file: %v", err)
	}

	fmt.Printf("Renamed: %s -> %s\n", filepath.Base(filePath), newName)
	return nil
}

// ParseCompletion parses the completion text into a ReceiptInfo struct
func ParseCompletion(completion string) (ReceiptInfo, error) {
	lines := strings.Split(completion, "\n")
	info := ReceiptInfo{}

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		switch key {
		case "Date":
			date, err := time.Parse("2006-01-02", value)
			if err != nil {
				return ReceiptInfo{}, fmt.Errorf("invalid date format: %v", err)
			}
			info.StartDate = date
			info.EndDate = date
		case "Start Date", "StartDate":
			date, err := time.Parse("2006-01-02", value)
			if err != nil {
				return ReceiptInfo{}, fmt.Errorf("invalid start date format: %v", err)
			}
			info.StartDate = date
		case "End Date", "EndDate":
			date, err := time.Parse("2006-01-02", value)
			if err != nil {
				return ReceiptInfo{}, fmt.Errorf("invalid end date format: %v", err)
			}
			info.EndDate = date
		case "Total":
			total, err := strconv.ParseFloat(value, 64)
			if err != nil {
				return ReceiptInfo{}, fmt.Errorf("invalid total amount: %v", err)
			}
			info.Total = total
		case "Vendor":
			info.Vendor = value
		case "Category":
			info.Category = value
		}
	}

	// Validate required fields
	if info.StartDate.IsZero() {
		return ReceiptInfo{}, fmt.Errorf("missing required field: Date or StartDate")
	}
	if info.EndDate.IsZero() {
		info.EndDate = info.StartDate
	}
	if info.Total == 0 {
		return ReceiptInfo{}, fmt.Errorf("missing required field: Total")
	}
	if info.Vendor == "" {
		return ReceiptInfo{}, fmt.Errorf("missing required field: Vendor")
	}
	if info.Category == "" {
		return ReceiptInfo{}, fmt.Errorf("missing required field: Category")
	}

	return info, nil
}

// GenerateFilename generates a filename from ReceiptInfo
func GenerateFilename(info ReceiptInfo) string {
	// Format dates for filename
	startDate := info.StartDate.Format("01-02-2006")
	endDate := info.EndDate.Format("01-02-2006")

	// Only include "to" if dates are different
	datePart := startDate
	if !info.StartDate.Equal(info.EndDate) {
		datePart = fmt.Sprintf("%s to %s", startDate, endDate)
	}

	// Replace spaces with underscores and handle commas in categories
	vendorPart := strings.ReplaceAll(info.Vendor, " ", "_")
	categoryPart := strings.ReplaceAll(info.Category, " ", "_")
	categoryPart = strings.ReplaceAll(categoryPart, ",", ",_")

	return fmt.Sprintf("%s - %.2f - %s - %s.pdf",
		datePart,
		info.Total,
		vendorPart,
		categoryPart)
}

// IsOllamaRunning checks if the Ollama service is running
func IsOllamaRunning() bool {
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

// IsModelAvailable checks if a specific model is available in Ollama
func IsModelAvailable(model string) bool {
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
