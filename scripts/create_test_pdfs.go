package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jung-kurt/gofpdf"
)

func main() {
	// Get the current working directory
	projectRoot, err := os.Getwd()
	if err != nil {
		fmt.Printf("Error getting current directory: %v\n", err)
		os.Exit(1)
	}
	// Go up one level from scripts directory
	projectRoot = filepath.Dir(projectRoot)

	// Create testdata directory if it doesn't exist
	testdataDir := filepath.Join(projectRoot, "testdata")
	if err := os.MkdirAll(testdataDir, 0755); err != nil {
		fmt.Printf("Error creating testdata directory: %v\n", err)
		os.Exit(1)
	}

	// Create regular receipt PDF
	regularContent := `Test Store
123 Main St
Anytown, USA

Date: 01/15/2023
Total: $123.45

Food
Tax: $10.00
Total: $123.45

Thank you for shopping at Test Store!`

	if err := createPDF(regularContent, filepath.Join(testdataDir, "regular_receipt.pdf")); err != nil {
		fmt.Printf("Error creating regular receipt PDF: %v\n", err)
		os.Exit(1)
	}

	// Create hotel receipt PDF
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

	if err := createPDF(hotelContent, filepath.Join(testdataDir, "hotel_receipt.pdf")); err != nil {
		fmt.Printf("Error creating hotel receipt PDF: %v\n", err)
		os.Exit(1)
	}

	// Save the content as text files for reference
	if err := os.WriteFile(filepath.Join(testdataDir, "regular_receipt.txt"), []byte(regularContent), 0644); err != nil {
		fmt.Printf("Error saving regular receipt text: %v\n", err)
		os.Exit(1)
	}

	if err := os.WriteFile(filepath.Join(testdataDir, "hotel_receipt.txt"), []byte(hotelContent), 0644); err != nil {
		fmt.Printf("Error saving hotel receipt text: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Test PDFs created successfully in", testdataDir)
}

func createPDF(content string, outputPath string) error {
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
