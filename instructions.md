# RcptPixie - PDF Receipt Renamer

## Overview
RcptPixie is a command-line application that automatically renames PDF receipt files using AI-powered text extraction. The app uses Ollama to communicate with a local LLM model to extract key information from receipts.

## Requirements
- Go 1.21 or later
- [Ollama](https://ollama.ai/) installed and running
- A compatible LLM model (default: llama3.2)

## Project Structure
```
rcptpixie/
├── cmd/
│   └── rcptpixie/          # Command-line interface
│       ├── main.go         # Entry point
│       └── main_test.go    # CLI tests
├── internal/
│   └── rcptpixie/          # Core functionality
│       ├── rcptpixie.go    # Main implementation
│       ├── rcptpixie_test.go    # Unit tests
│       └── integration_test.go   # Integration tests
├── version/               # Version information
├── go.mod
└── go.sum
```

## Implementation Details

### File Naming Format
- Regular receipts: `MM-DD-YYYY - AMOUNT - VENDOR - CATEGORY.pdf`
  - Example: `04-16-2025 - 102.11 - Revier Bistro - Food.pdf`
- Hotel receipts: `MM-DD-YYYY to MM-DD-YYYY - AMOUNT - VENDOR - CATEGORY.pdf`
  - Example: `04-02-2025 to 04-10-2025 - 2006.33 - Four Seasons - Lodging.pdf`

### Core Components

1. **PDF Text Extraction**
   - Use a PDF library to extract text content
   - Handle multi-page PDFs
   - Clean and normalize extracted text

2. **LLM Integration**
   - Use Ollama's HTTP API to communicate with the LLM
   - Default model: llama3.2
   - Allow model selection via command-line parameter
   - Implement proper error handling for model availability

3. **Information Extraction**
   - Extract the following fields:
     - Date (or start/end dates for hotel receipts)
     - Total amount (numeric only, no currency symbols)
     - Vendor name
     - Category (single most appropriate category)
   - Handle various receipt formats and currencies
   - Validate extracted information

4. **File Renaming**
   - Generate new filename based on extracted information
   - Handle spaces and special characters in vendor names
   - Preserve original file extension
   - Implement proper error handling for file operations

### Command Line Interface
```bash
# Show help information
rcptpixie
rcptpixie -help

# Process a single PDF file
rcptpixie /path/to/receipt.pdf

# Process all PDF files in a directory
rcptpixie /path/to/receipts/

# Specify a different Ollama model
rcptpixie -model llama3.2 /path/to/receipt.pdf

# Show version information
rcptpixie -version

# Enable verbose logging
rcptpixie -verbose /path/to/receipt.pdf
```

### LLM Prompt
The prompt should instruct the LLM to:
1. Extract only the required information
2. Format dates as YYYY-MM-DD
3. Return numeric values only (no currency symbols)
4. Choose a single most appropriate category
5. Handle both regular and hotel receipts
6. Return empty fields if information cannot be determined

Example prompt:
```
You are a helpful assistant that extracts information from receipts. Please analyze this receipt and extract the following information:

Receipt Text:
[EXTRACTED_TEXT]

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
7. For category, choose ONE most appropriate category
8. Do not include any additional text or notes in the output
```

### Error Handling
Implement proper error handling for:
- Missing or invalid PDF files
- Ollama connection issues
- Missing or unavailable models
- Invalid receipt formats
- Insufficient receipt information
- File system operations

### Testing
1. Unit Tests
   - Test individual components
   - Mock external dependencies
   - Test error conditions

2. Integration Tests
   - Test the complete workflow
   - Require Ollama to be running
   - Test with various receipt formats
   - Verify file renaming functionality

### Dependencies
- PDF text extraction library
- HTTP client for Ollama API
- Testing framework
- Command-line argument parsing

### Building and Installation
```bash
# Build the binary
go build -o rcptpixie ./cmd/rcptpixie

# Install to $GOPATH/bin
go install ./cmd/rcptpixie
```

### Development Workflow
1. Set up development environment
2. Implement core functionality
3. Add tests
4. Implement CLI interface
5. Add error handling
6. Test with various receipt formats
7. Document the code
8. Create installation instructions