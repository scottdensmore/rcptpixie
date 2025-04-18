# RcptPixie

A command-line tool that automatically renames PDF receipts using AI-powered text extraction.

## Features

- Extracts text from PDF receipts
- Uses Ollama's LLM to identify key information
- Automatically renames files with consistent formatting
- Supports both single files and directories
- Cross-platform support (Windows, macOS, Linux)
- Version information tracking

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

## Prerequisites

- [Ollama](https://ollama.ai/) installed and running locally
- A compatible LLM model (default: llama3.2)

## Usage

```bash
# Process a single PDF file
rcptpixie /path/to/receipt.pdf

# Process all PDF files in a directory
rcptpixie /path/to/receipts/

# Specify a different Ollama model
rcptpixie -model llama3.2 /path/to/receipt.pdf

# Show version information
rcptpixie -version
```

## File Naming Format

Receipts are renamed using the following format:
- Regular receipts: `MM-DD-YYYY - AMOUNT - VENDOR - CATEGORY.pdf`
- Hotel receipts: `MM-DD-YYYY to MM-DD-YYYY - AMOUNT - VENDOR - CATEGORY.pdf`

## License

MIT License - see LICENSE file for details 