package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/jung-kurt/gofpdf"
)

func main() {
	// Create testdata directory if it doesn't exist
	if err := os.MkdirAll("testdata", 0755); err != nil {
		fmt.Printf("Error creating testdata directory: %v\n", err)
		os.Exit(1)
	}

	// Create regular receipt PDF
	if err := createPDF("testdata/regular_receipt.txt", "testdata/regular_receipt.pdf"); err != nil {
		fmt.Printf("Error creating regular receipt PDF: %v\n", err)
		os.Exit(1)
	}

	// Create hotel receipt PDF
	if err := createPDF("testdata/hotel_receipt.txt", "testdata/hotel_receipt.pdf"); err != nil {
		fmt.Printf("Error creating hotel receipt PDF: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Test PDFs created successfully")
}

func createPDF(inputPath, outputPath string) error {
	// Read the input text file
	content, err := os.ReadFile(inputPath)
	if err != nil {
		return fmt.Errorf("error reading input file: %v", err)
	}

	// Create a new PDF
	pdf := gofpdf.New("P", "mm", "A4", "")
	pdf.AddPage()
	pdf.SetFont("Arial", "", 12)

	// Write the content to the PDF
	for _, line := range strings.Split(string(content), "\n") {
		pdf.MultiCell(0, 10, line, "", "", false)
	}

	// Save the PDF
	if err := pdf.OutputFileAndClose(outputPath); err != nil {
		return fmt.Errorf("error saving PDF: %v", err)
	}

	return nil
}
