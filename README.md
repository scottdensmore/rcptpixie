# RCPTPixie

RCPTPixie is a command-line tool that automatically renames PDF receipt files using AI-powered analysis. It uses Ollama to extract information from receipts and rename them in a consistent format.

## Prerequisites

- Go 1.21 or later
- Ollama installed and running locally
- The desired LLM model (default: llama3.3) pulled in Ollama

## Installation

1. Clone this repository
2. Run `go mod tidy` to install dependencies
3. Build the application:
   ```bash
   go build
   ```

## Usage

Basic usage:
```bash
./rcptpixie [options] <pdf_file1> [pdf_file2 ...]
```

Options:
- `-model`: Specify the Ollama model to use (default: llama3.3)

Examples:
```bash
# Process a single receipt
./rcptpixie receipt.pdf

# Process multiple receipts
./rcptpixie receipt1.pdf receipt2.pdf

# Use a different model
./rcptpixie -model mistral receipt.pdf
```

## Output Format

The tool renames files in the following format:
- Regular receipts: `MM-DD-YYYY - Total - Vendor - Category.pdf`
- Hotel receipts: `MM-DD-YYYY to MM-DD-YYYY - Total - Vendor - Category.pdf`

Example:
- `04-16-2025 - 102.11 - Revier_Bistro - Food.pdf`
- `04-02-2025 to 04-10-2025 - 2006.33 - Four_Seasons - Lodging.pdf`

## Notes

- The tool requires Ollama to be running locally
- The LLM model should be capable of understanding and extracting information from PDF receipts
- The tool will preserve the original file's directory location 