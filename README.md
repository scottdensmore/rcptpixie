# RcptPixie

A command-line tool that automatically renames PDF receipts using AI-powered text extraction.

## Features

- Extracts text from PDF receipts
- Uses Ollama's LLM to identify key information
- Automatically renames files with consistent formatting
- Supports both single files and directories
- Cross-platform support (Windows, macOS, Linux)
- Version information tracking

## Prerequisites

- Go 1.21 or later
- [Ollama](https://ollama.ai/) installed and running
- The llama2 model installed in Ollama

## Installation

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
rcptpixie -model llama2:latest /path/to/receipt.pdf

# Show version information
rcptpixie -version

# Enable verbose logging for debugging
rcptpixie -verbose /path/to/receipt.pdf
```

### Command Line Options

- `-help`: Show help information
- `-model`: Specify the Ollama model to use (default: "llama2:latest")
- `-version`: Show version information
- `-verbose`: Enable detailed logging for debugging (shows processing steps, PDF extraction details, and LLM interactions)

## File Naming Format

Receipts are renamed using the following format:
- Regular receipts: `MM-DD-YYYY - AMOUNT - VENDOR - CATEGORY.pdf`
- Hotel receipts: `MM-DD-YYYY to MM-DD-YYYY - AMOUNT - VENDOR - CATEGORY.pdf`

## Development

### Running Tests

#### Unit Tests
```bash
go test -v
```

#### Integration Tests
The integration tests require Ollama to be running and the llama2 model to be installed. To run the integration tests:

1. Make sure Ollama is running:
   ```bash
   ollama serve
   ```

2. Install the llama2 model if not already installed:
   ```bash
   ollama pull llama2
   ```

3. Run the integration tests:
   ```bash
   go test -v -run TestPDFProcessing
   ```

To skip integration tests (e.g., in CI environments), set the `SKIP_INTEGRATION_TESTS` environment variable:
```bash
SKIP_INTEGRATION_TESTS=1 go test -v
```

### Building

```bash
go build
```

## License

MIT License - see LICENSE file for details 