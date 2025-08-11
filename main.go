package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"markdown-images/markdown"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: go run main.go <markdown-file> [--debug]")
		os.Exit(1)
	}

	inputFile := os.Args[1]
	debugMode := len(os.Args) > 2 && os.Args[2] == "--debug"

	content, err := os.ReadFile(inputFile)
	if err != nil {
		log.Fatalf("Error reading file %s: %v", inputFile, err)
	}

	processedContent, err := markdown.ProcessMarkdown(string(content), filepath.Dir(inputFile), debugMode)
	if err != nil {
		log.Fatalf("Error processing markdown: %v", err)
	}

	outputFile := strings.TrimSuffix(inputFile, filepath.Ext(inputFile)) + "_embedded.md"
	err = os.WriteFile(outputFile, []byte(processedContent), 0644)
	if err != nil {
		log.Fatalf("Error writing output file %s: %v", outputFile, err)
	}

	fmt.Printf("Successfully processed %s -> %s\n", inputFile, outputFile)
}
