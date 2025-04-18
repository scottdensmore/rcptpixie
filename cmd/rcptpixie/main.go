package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/scottdensmore/rcptpixie/internal/rcptpixie"
	"github.com/scottdensmore/rcptpixie/version"
)

func parseFlags(args []string) (model string, showVersion bool, showHelp bool, verbose bool) {
	// Create a new flag set
	fs := flag.NewFlagSet("rcptpixie", flag.ExitOnError)

	// Define flags
	modelName := fs.String("model", "llama3.2", "Ollama model to use")
	versionFlag := fs.Bool("version", false, "Show version information")
	helpFlag := fs.Bool("help", false, "Show help information")
	verboseFlag := fs.Bool("verbose", false, "Enable verbose logging")

	// Set custom usage
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: %s [options] <pdf-file-or-directory>\n\n", os.Args[0])
		fmt.Fprintf(fs.Output(), "Options:\n")
		fs.PrintDefaults()
		fmt.Fprintf(fs.Output(), "\nExamples:\n")
		fmt.Fprintf(fs.Output(), "  %s receipt.pdf\n", os.Args[0])
		fmt.Fprintf(fs.Output(), "  %s -model llama3.2 ./receipts/\n", os.Args[0])
		fmt.Fprintf(fs.Output(), "  %s -verbose receipt.pdf\n", os.Args[0])
	}

	// Parse flags
	if err := fs.Parse(args); err != nil {
		fs.Usage()
		os.Exit(1)
	}

	return *modelName, *versionFlag, *helpFlag, *verboseFlag
}

func main() {
	// Parse command line flags
	modelName, showVersion, showHelp, verbose := parseFlags(os.Args[1:])

	// Initialize logger
	rcptpixie.InitLogger(verbose)

	// Show help if requested or no arguments provided
	if showHelp || (len(flag.Args()) == 0 && !showVersion) {
		flag.Usage()
		return
	}

	// Show version if requested
	if showVersion {
		fmt.Println(version.Get().String())
		return
	}

	// Get input path from command line arguments
	args := flag.Args()
	if len(args) == 0 {
		log.Fatal("No input file or directory specified")
	}
	inputPath := args[0]

	rcptpixie.Log.Printf("Starting processing with model: %s", modelName)
	rcptpixie.Log.Printf("Processing input path: %s", inputPath)

	// Check if the input is a directory
	info, err := os.Stat(inputPath)
	if err != nil {
		log.Fatalf("Error accessing path %s: %v", inputPath, err)
	}

	if info.IsDir() {
		rcptpixie.Log.Printf("Processing directory: %s", inputPath)
		// Process all PDF files in the directory
		err := filepath.Walk(inputPath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if !info.IsDir() && strings.HasSuffix(strings.ToLower(path), ".pdf") {
				rcptpixie.Log.Printf("Found PDF file: %s", path)
				if err := rcptpixie.ProcessFile(path, modelName); err != nil {
					log.Printf("Error processing %s: %v", path, err)
				}
			}
			return nil
		})
		if err != nil {
			log.Fatalf("Error walking directory %s: %v", inputPath, err)
		}
	} else {
		rcptpixie.Log.Printf("Processing single file: %s", inputPath)
		// Process single file
		if err := rcptpixie.ProcessFile(inputPath, modelName); err != nil {
			log.Fatalf("Error processing %s: %v", inputPath, err)
		}
	}
}
