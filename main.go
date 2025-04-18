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
	"strings"
	"time"

	"github.com/dslipak/pdf"
)

type ReceiptInfo struct {
	Date      string
	Total     string
	Vendor    string
	Category  string
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
	flag.Parse()

	// Get input path from command line arguments
	args := flag.Args()
	if len(args) == 0 {
		log.Fatal("Please provide a PDF file or directory to process")
	}

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

	// Log extracted text for debugging
	log.Printf("Extracted text from PDF:\n%s", textContent)

	// Create prompt for the LLM
	prompt := fmt.Sprintf(`You are a helpful assistant that extracts information from receipts. Please analyze this existing receipt and extract the following information:

Receipt Text:
%s

Please extract and provide ONLY the following information in this exact format:
Date: MM/DD/YYYY
Total: XXXX.XX
Vendor: Name
Category: Type
Stay Dates: MM/DD/YYYY to MM/DD/YYYY (only if this is a hotel receipt)

If any field cannot be determined from the receipt, leave it empty but keep the label. For example:
Date: 
Total: 123.45
Vendor: Unknown Store
Category: Food`, textContent)

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
		return fmt.Errorf("error sending request to Ollama: %v", err)
	}
	defer resp.Body.Close()

	// Read response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("error reading response: %v", err)
	}

	// Log the raw response for debugging
	log.Printf("Raw Ollama response: %s", string(body))

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
				log.Printf("Error parsing streaming response line: %v", err)
				continue
			}
			fullResponse.WriteString(streamResp.Response)
			if streamResp.Done {
				break
			}
		}
		ollamaResp.Response = fullResponse.String()
	}

	// Log the parsed response for debugging
	log.Printf("Parsed response: %s", ollamaResp.Response)

	// Parse the response and create ReceiptInfo
	info := parseLLMResponse(ollamaResp.Response)

	// Skip renaming if we don't have enough information
	if info.Date == "" && info.Total == "" && info.Vendor == "" && info.Category == "" {
		return fmt.Errorf("could not extract enough information from the receipt")
	}

	// Generate new filename
	newName := generateFilename(info)

	// Rename the file
	dir := filepath.Dir(filePath)
	newPath := filepath.Join(dir, newName)
	if err := os.Rename(filePath, newPath); err != nil {
		return fmt.Errorf("error renaming file: %v", err)
	}

	fmt.Printf("Renamed: %s -> %s\n", filepath.Base(filePath), newName)
	return nil
}

func parseLLMResponse(response string) ReceiptInfo {
	info := ReceiptInfo{}

	lines := strings.Split(response, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Date:") {
			info.Date = strings.TrimSpace(strings.TrimPrefix(line, "Date:"))
		} else if strings.HasPrefix(line, "Total:") {
			info.Total = strings.TrimSpace(strings.TrimPrefix(line, "Total:"))
		} else if strings.HasPrefix(line, "Vendor:") {
			info.Vendor = strings.TrimSpace(strings.TrimPrefix(line, "Vendor:"))
		} else if strings.HasPrefix(line, "Category:") {
			info.Category = strings.TrimSpace(strings.TrimPrefix(line, "Category:"))
		} else if strings.HasPrefix(line, "Stay Dates:") {
			info.StayDates = strings.TrimSpace(strings.TrimPrefix(line, "Stay Dates:"))
			info.IsHotel = true
		}
	}

	return info
}

func generateFilename(info ReceiptInfo) string {
	// Format the date
	date, err := time.Parse("01/02/2006", info.Date)
	if err != nil {
		date = time.Now() // Fallback to current date if parsing fails
	}
	formattedDate := date.Format("01-02-2006")

	// Clean up the vendor name
	vendor := strings.ReplaceAll(info.Vendor, " ", "_")

	// Generate the filename
	if info.IsHotel {
		return fmt.Sprintf("%s to %s - %s - %s - %s.pdf",
			formattedDate, info.StayDates, info.Total, vendor, info.Category)
	}

	return fmt.Sprintf("%s - %s - %s - %s.pdf",
		formattedDate, info.Total, vendor, info.Category)
}
