# RcptPixie

[![Test](https://github.com/scottdensmore/rcptpixie/actions/workflows/test.yml/badge.svg)](https://github.com/scottdensmore/rcptpixie/actions/workflows/test.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/scottdensmore/rcptpixie)](https://goreportcard.com/report/github.com/scottdensmore/rcptpixie)

A command-line tool that automatically renames PDF receipts using AI-powered text extraction.

## Features

- Extracts text from PDF receipts
- Uses Ollama's LLM to identify key information
- Automatically renames files with consistent formatting
- Supports both single files and directories
- Cross-platform support (Windows, macOS, Linux)
- Version information tracking
- Handles both regular and hotel receipts
- Supports numbers with commas in totals (e.g., $1,234.56)

## Prerequisites

- Go 1.21 or later
- [Ollama](https://ollama.ai/) installed and running
- The llama3.2 model installed in Ollama (default) or any other compatible model

## Installation

### From Source

```bash
# Clone the repository
git clone https://github.com/scottdensmore/rcptpixie.git
cd rcptpixie

# Build and install
go install ./cmd/rcptpixie
```

### From GitHub Releases

1. Visit the [Releases page](https://github.com/scottdensmore/rcptpixie/releases)
2. Download the appropriate archive for your platform
3. Extract the archive
4. Move the binary to a directory in your PATH (optional)

### macOS

```bash
# Using Homebrew
brew tap scottdensmore/tap
brew install rcptpixie
```

### Linux

```bash
# Download and install
curl -L https://github.com/scottdensmore/rcptpixie/releases/latest/download/rcptpixie_Linux_x86_64.tar.gz | tar xz
sudo mv rcptpixie /usr/local/bin/
```

### Windows

1. Download the latest Windows release
2. Extract the ZIP file
3. Add the directory containing rcptpixie.exe to your PATH

## Usage

```bash
# Show help information (default when no arguments provided)
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

# Enable verbose logging for debugging
rcptpixie -verbose /path/to/receipt.pdf
```

### Command Line Options

- `-help`: Show help information
- `-model`: Specify the Ollama model to use (default: "llama3.2")
- `-version`: Show version information
- `-verbose`: Enable detailed logging for debugging (shows processing steps, PDF extraction details, and LLM interactions)

## File Naming Format

Receipts are renamed using the following format:
- Regular receipts: `MM-DD-YYYY - AMOUNT - VENDOR - CATEGORY.pdf`
- Hotel receipts: `MM-DD-YYYY to MM-DD-YYYY - AMOUNT - VENDOR - CATEGORY.pdf`

Examples:
- `01-15-2023 - 123.45 - Test_Store - Food.pdf`
- `01-15-2023 to 01-17-2023 - 500.00 - Grand_Hotel - Lodging.pdf`
- `01-15-2023 - 17830.81 - Test_Store - Food_&_Drink.pdf`

## Project Structure

```
rcptpixie/
├── cmd/
│   └── rcptpixie/          # Command-line interface
│       ├── main.go
│       └── main_test.go
├── internal/
│   └── rcptpixie/          # Core functionality
│       ├── rcptpixie.go
│       ├── main_test.go
│       └── main_integration_test.go
├── scripts/                # Utility scripts
├── version/               # Version information
├── go.mod
└── go.sum
```

## Development

### Running Tests

#### Unit Tests
```bash
go test ./... -v
```

#### Integration Tests
The integration tests require Ollama to be running and the llama3.2 model to be installed. To run the integration tests:

1. Make sure Ollama is running:
   ```bash
   ollama serve
   ```

2. Install the llama3.2 model if not already installed:
   ```bash
   ollama pull llama3.2
   ```

3. Run the integration tests:
   ```bash
   go test -v ./... -run TestPDFProcessing
   ```

To skip integration tests (e.g., in CI environments), set the `SKIP_INTEGRATION_TESTS` environment variable:
```bash
SKIP_INTEGRATION_TESTS=1 go test ./... -v
```

### Building

```bash
# Build the binary
go build -o rcptpixie ./cmd/rcptpixie

# Install to $GOPATH/bin
go install ./cmd/rcptpixie
```

### Error Handling

The tool provides clear error messages for common issues:
- Missing or invalid PDF files
- Ollama connection issues
- Missing or unavailable models
- Invalid receipt formats
- Insufficient receipt information

## License

MIT License - see LICENSE file for details 