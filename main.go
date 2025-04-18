package main

import (
	"bytes"
	"encoding/json"
	"flag"
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
	"github.com/scottdensmore/rcptpixie/version"
)

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

type OllamaRequest struct {
	Model   string                 `json:"model"`
	Prompt  string                 `json:"prompt"`
	Options map[string]interface{} `json:"options"`
}

type OllamaResponse struct {
	Response string `json:"response"`
}

func main() {
	// Parse command line flags
	modelName := flag.String("model", "llama3.2", "Ollama model to use")
	showVersion := flag.Bool("version", false, "Show version information")
	showHelp := flag.Bool("help", false, "Show help information")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: %s [options] <pdf-file-or-directory>\n\n", os.Args[0])
		fmt.Fprintf(flag.CommandLine.Output(), "Options:\n")
		flag.PrintDefaults()
		fmt.Fprintf(flag.CommandLine.Output(), "\nExamples:\n")
		fmt.Fprintf(flag.CommandLine.Output(), "  %s receipt.pdf\n", os.Args[0])
		fmt.Fprintf(flag.CommandLine.Output(), "  %s -model llama3.2 ./receipts/\n", os.Args[0])
	}
	flag.Parse()

	// Show help if requested or no arguments provided
	if *showHelp || (len(flag.Args()) == 0 && !*showVersion) {
		flag.Usage()
		return
	}

	// Show version if requested
	if *showVersion {
		fmt.Println(version.Get().String())
		return
	}

	// Get input path from command line arguments
	args := flag.Args()
	inputPath := args[0]

	// Check if the input is a directory
	info, err := os.Stat(inputPath)
	if err != nil {
		log.Fatalf("Error accessing path %s: %v", inputPath, err)
	}

	if info.IsDir() {
		// Process all PDF files in the directory
		err := filepath.Walk(inputPath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if !info.IsDir() && strings.HasSuffix(strings.ToLower(path), ".pdf") {
				if err := processFile(path, *modelName); err != nil {
					log.Printf("Error processing %s: %v", path, err)
				}
			}
			return nil
		})
		if err != nil {
			log.Fatalf("Error walking directory %s: %v", inputPath, err)
		}
	} else {
		// Process single file
		if err := processFile(inputPath, *modelName); err != nil {
			log.Fatalf("Error processing %s: %v", inputPath, err)
		}
	}
}

func extractTextFromPDF(filePath string) (string, error) {
	// Open the PDF file
	r, err := pdf.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("error opening PDF: %v", err)
	}

	var textBuilder strings.Builder
	// Extract text from all pages
	for i := 1; i <= r.NumPage(); i++ {
		p := r.Page(i)
		if p.V.IsNull() {
			continue
		}
		text, err := p.GetPlainText(nil)
		if err != nil {
			return "", fmt.Errorf("error extracting text from page %d: %v", i, err)
		}
		textBuilder.WriteString(text)
		textBuilder.WriteString("\n")
	}

	return textBuilder.String(), nil
}

func processFile(filePath string, modelName string) error {
	// Verify file exists and is a PDF
	if !strings.HasSuffix(strings.ToLower(filePath), ".pdf") {
		return fmt.Errorf("file %s is not a PDF", filePath)
	}

	// Extract text from PDF
	textContent, err := extractTextFromPDF(filePath)
	if err != nil {
		return fmt.Errorf("error extracting text from PDF: %v", err)
	}

	// Create prompt for the LLM
	prompt := fmt.Sprintf(`You are a helpful assistant that extracts information from receipts. Please analyze this existing receipt and extract the following information:

Receipt Text:
%s

Please extract and provide ONLY the following information in this exact format:
Date: MM/DD/YYYY (for regular receipts)
Check-in Date: MM/DD/YYYY (for hotel receipts)
Check-out Date: MM/DD/YYYY (for hotel receipts)
Total: XXXX.XX
Vendor: Name
Category: Type

If any field cannot be determined from the receipt, leave it empty but keep the label. For example:
Date: 
Total: 123.45
Vendor: Unknown Store
Category: Food

For hotel receipts, use Check-in Date and Check-out Date instead of Date.`, textContent)

	// Create request to Ollama
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
	resp, err := http.Post("http://localhost:11434/api/generate", "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("error connecting to Ollama: %v. Make sure Ollama is running and the model '%s' is available", err, modelName)
	}
	defer resp.Body.Close()

	// Check for model not found error
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("model '%s' not found. Please make sure the model is available in Ollama", modelName)
	}

	// Read response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("error reading response: %v", err)
	}

	var ollamaResp OllamaResponse
	if err := json.Unmarshal(body, &ollamaResp); err != nil {
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

	// Parse the response and create ReceiptInfo
	info, err := parseCompletion(ollamaResp.Response)
	if err != nil {
		return fmt.Errorf("error parsing completion: %v", err)
	}

	// Skip renaming if we don't have enough information
	if info.Date.IsZero() && info.Total == 0 && info.Vendor == "" && info.Category == "" {
		return fmt.Errorf("could not extract enough information from the receipt")
	}

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

	newName := fmt.Sprintf("%s - %.2f - %s - %s%s",
		datePart,
		info.Total,
		strings.ReplaceAll(info.Vendor, " ", "_"),
		strings.ReplaceAll(info.Category, " ", "_"),
		ext)

	// Rename the file
	newPath := filepath.Join(dir, newName)
	if err := os.Rename(filePath, newPath); err != nil {
		return fmt.Errorf("error renaming file: %v", err)
	}

	fmt.Printf("Renamed: %s -> %s\n", filepath.Base(filePath), newName)
	return nil
}

func parseCompletion(completion string) (ReceiptInfo, error) {
	info := ReceiptInfo{}
	lines := strings.Split(completion, "\n")

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
			if value == "" {
				continue
			}
			date, err := time.Parse("01/02/2006", value)
			if err != nil {
				return info, fmt.Errorf("invalid date format: %v", err)
			}
			info.StartDate = date
			info.EndDate = date
		case "Check-in Date":
			if value == "" {
				continue
			}
			date, err := time.Parse("01/02/2006", value)
			if err != nil {
				return info, fmt.Errorf("invalid check-in date format: %v", err)
			}
			info.StartDate = date
			// If we don't find a check-out date, use the check-in date
			info.EndDate = date
		case "Check-out Date":
			if value == "" {
				continue
			}
			date, err := time.Parse("01/02/2006", value)
			if err != nil {
				return info, fmt.Errorf("invalid check-out date format: %v", err)
			}
			info.EndDate = date
		case "Total":
			if value == "" {
				continue
			}
			// Remove currency symbol and commas
			value = strings.TrimPrefix(value, "$")
			value = strings.ReplaceAll(value, ",", "")
			total, err := strconv.ParseFloat(value, 64)
			if err != nil {
				return info, fmt.Errorf("invalid total format: %v", err)
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
		return info, fmt.Errorf("no valid date found in receipt")
	}
	if info.Total == 0 {
		return info, fmt.Errorf("no valid total found in receipt")
	}
	if info.Vendor == "" {
		return info, fmt.Errorf("no vendor found in receipt")
	}
	if info.Category == "" {
		return info, fmt.Errorf("no category found in receipt")
	}

	return info, nil
}

func generateFilename(info ReceiptInfo) string {
	var dateStr string
	if info.StartDate.Equal(info.EndDate) {
		dateStr = info.StartDate.Format("01-02-2006")
	} else {
		dateStr = fmt.Sprintf("%s to %s",
			info.StartDate.Format("01-02-2006"),
			info.EndDate.Format("01-02-2006"))
	}

	vendor := strings.ReplaceAll(info.Vendor, " ", "_")
	category := strings.ReplaceAll(info.Category, " ", "_")

	return fmt.Sprintf("%s - %.2f - %s - %s.pdf",
		dateStr,
		info.Total,
		vendor,
		category)
}
