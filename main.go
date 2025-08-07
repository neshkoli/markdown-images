package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/image/draw"
)

// ImageReference represents an image reference found in markdown or HTML
type ImageReference struct {
	FullMatch string
	AltText   string
	ImagePath string
	StartPos  int
	EndPos    int
	Width     int
	Height    int
	IsHTML    bool
}

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

	processedContent, err := processMarkdown(string(content), filepath.Dir(inputFile), debugMode)
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

func processMarkdown(content, baseDir string, debugMode bool) (string, error) {
	imageRefs := findImageReferences(content)
	sort.Slice(imageRefs, func(i, j int) bool {
		return imageRefs[i].StartPos < imageRefs[j].StartPos
	})

	var builder strings.Builder
	lastIndex := 0

	for _, imgRef := range imageRefs {
		builder.WriteString(content[lastIndex:imgRef.StartPos])

		if debugMode {
			log.Printf("Processing image: %s, Width: %d, Height: %d", imgRef.ImagePath, imgRef.Width, imgRef.Height)
		}

		base64Data, mimeType, err := imageToBase64(imgRef, baseDir, debugMode)
		if err != nil {
			log.Printf("Warning: Could not convert image %s to base64: %v. Keeping original reference.", imgRef.ImagePath, err)
			builder.WriteString(imgRef.FullMatch)
		} else {
			newImageRef := fmt.Sprintf("![%s](data:%s;base64,%s)", imgRef.AltText, mimeType, base64Data)
			builder.WriteString(newImageRef)
		}
		lastIndex = imgRef.EndPos
	}

	builder.WriteString(content[lastIndex:])
	return builder.String(), nil
}

func findImageReferences(content string) []ImageReference {
	var refs []ImageReference
	// Regex for Markdown: ![alt](path){: width=W height=H}
	markdownRegex := regexp.MustCompile(`!\[([^\]]*)\]\(([^)]+?)\)(?:\{:\s*(?:width=(\d+))?\s*(?:height=(\d+))?\s*\})?`)
	// Regex for HTML: <img src="..." alt="..." width="..." height="...">
	htmlRegex := regexp.MustCompile(`<img[^>]+src=["']([^"']+)["'][^>]*alt=["']([^"']*)["'][^>]*>`)

	// Process Markdown matches
	for _, match := range markdownRegex.FindAllStringSubmatchIndex(content, -1) {
		imagePath := content[match[4]:match[5]]
		if strings.HasPrefix(imagePath, "data:") {
			continue
		}

		var width, height int
		if match[6] != -1 && match[7] != -1 {
			width, _ = strconv.Atoi(content[match[6]:match[7]])
		}
		if match[8] != -1 && match[9] != -1 {
			height, _ = strconv.Atoi(content[match[8]:match[9]])
		}

		refs = append(refs, ImageReference{
			FullMatch: content[match[0]:match[1]],
			AltText:   content[match[2]:match[3]],
			ImagePath: imagePath,
			StartPos:  match[0],
			EndPos:    match[1],
			Width:     width,
			Height:    height,
		})
	}

	// Process HTML matches
	htmlWidthRegex := regexp.MustCompile(`width=["'](\d+)["']`)
	htmlHeightRegex := regexp.MustCompile(`height=["'](\d+)["']`)
	for _, match := range htmlRegex.FindAllStringSubmatchIndex(content, -1) {
		fullMatch := content[match[0]:match[1]]
		imagePath := content[match[2]:match[3]]
		if strings.HasPrefix(imagePath, "data:") {
			continue
		}
		altText := content[match[4]:match[5]]

		var width, height int
		widthMatch := htmlWidthRegex.FindStringSubmatch(fullMatch)
		if len(widthMatch) > 1 {
			width, _ = strconv.Atoi(widthMatch[1])
		}
		heightMatch := htmlHeightRegex.FindStringSubmatch(fullMatch)
		if len(heightMatch) > 1 {
			height, _ = strconv.Atoi(heightMatch[1])
		}

		refs = append(refs, ImageReference{
			FullMatch: fullMatch,
			AltText:   altText,
			ImagePath: imagePath,
			StartPos:  match[0],
			EndPos:    match[1],
			Width:     width,
			Height:    height,
			IsHTML:    true,
		})
	}

	return refs
}

func imageToBase64(ref ImageReference, baseDir string, debugMode bool) (string, string, error) {
	var content []byte
	var err error

	if isURL(ref.ImagePath) {
		content, err = downloadImageContent(ref.ImagePath)
		if err != nil {
			return "", "", fmt.Errorf("failed to download image: %v", err)
		}
	} else {
		fullPath := filepath.Join(baseDir, ref.ImagePath)
		content, err = os.ReadFile(fullPath)
		if err != nil {
			return "", "", fmt.Errorf("failed to read image file: %v", err)
		}
	}

	// Check for SVG first, as it's text-based
	if strings.Contains(strings.ToLower(string(content)), "<svg") {
		if ref.Width > 0 || ref.Height > 0 {
			content = updateSVGDimensions(content, ref.Width, ref.Height)
		}
		return base64.StdEncoding.EncodeToString(content), "image/svg+xml", nil
	}

	img, format, err := image.Decode(bytes.NewReader(content))
	if err != nil {
		return "", "", fmt.Errorf("unsupported image format: %v", err)
	}

	img = resizeImage(img, ref.Width, ref.Height)

	var encodeBuf bytes.Buffer
	var mimeType string
	switch format {
	case "png":
		mimeType = "image/png"
		err = png.Encode(&encodeBuf, img)
	case "gif":
		mimeType = "image/gif"
		err = gif.Encode(&encodeBuf, img, nil)
	default: // jpeg and others
		mimeType = "image/jpeg"
		err = jpeg.Encode(&encodeBuf, img, &jpeg.Options{Quality: 85})
	}

	if err != nil {
		return "", "", fmt.Errorf("failed to re-encode image: %v", err)
	}

	return base64.StdEncoding.EncodeToString(encodeBuf.Bytes()), mimeType, nil
}

func downloadImageContent(imageURL string) ([]byte, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(imageURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bad status: %s", resp.Status)
	}
	return io.ReadAll(resp.Body)
}

func resizeImage(img image.Image, targetWidth, targetHeight int) image.Image {
	srcWidth := img.Bounds().Dx()
	srcHeight := img.Bounds().Dy()

	if targetWidth <= 0 && targetHeight <= 0 {
		if srcWidth > 400 {
			targetWidth = 400
		} else {
			return img // No resize needed
		}
	}

	if targetWidth > 0 && targetHeight <= 0 {
		targetHeight = int(float64(targetWidth) * float64(srcHeight) / float64(srcWidth))
	} else if targetHeight > 0 && targetWidth <= 0 {
		targetWidth = int(float64(targetHeight) * float64(srcWidth) / float64(srcHeight))
	}

	if targetWidth == srcWidth && targetHeight == srcHeight {
		return img
	}

	resized := image.NewRGBA(image.Rect(0, 0, targetWidth, targetHeight))
	draw.ApproxBiLinear.Scale(resized, resized.Bounds(), img, img.Bounds(), draw.Over, nil)
	return resized
}

func updateSVGDimensions(content []byte, targetWidth, targetHeight int) []byte {
	// This is a simplified implementation. A more robust one would parse the SVG XML.
	widthRegex := regexp.MustCompile(`width=["']([^"']+)["']`)
	heightRegex := regexp.MustCompile(`height=["']([^"']+)["']`)
	contentStr := string(content)
	if targetWidth > 0 {
		contentStr = widthRegex.ReplaceAllString(contentStr, fmt.Sprintf(`width="%d"`, targetWidth))
	}
	if targetHeight > 0 {
		contentStr = heightRegex.ReplaceAllString(contentStr, fmt.Sprintf(`height="%d"`, targetHeight))
	}
	return []byte(contentStr)
}

func isURL(str string) bool {
	u, err := url.Parse(str)
	return err == nil && u.Scheme != "" && u.Host != ""
}
