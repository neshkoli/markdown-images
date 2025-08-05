package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
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
		fmt.Println("Example: go run main.go image-example.md")
		fmt.Println("Example: go run main.go image-example.md --debug")
		os.Exit(1)
	}

	inputFile := os.Args[1]
	debugMode := false

	// Check for debug flag
	if len(os.Args) > 2 && os.Args[2] == "--debug" {
		debugMode = true
	}

	// Read the markdown file
	content, err := os.ReadFile(inputFile)
	if err != nil {
		log.Fatalf("Error reading file %s: %v", inputFile, err)
	}

	// Process the markdown content
	processedContent, err := processMarkdown(string(content), filepath.Dir(inputFile), debugMode)
	if err != nil {
		log.Fatalf("Error processing markdown: %v", err)
	}

	// Create output filename
	outputFile := strings.TrimSuffix(inputFile, filepath.Ext(inputFile)) + "_embedded.md"

	// Write the processed content to output file
	err = os.WriteFile(outputFile, []byte(processedContent), 0644)
	if err != nil {
		log.Fatalf("Error writing output file %s: %v", outputFile, err)
	}

	fmt.Printf("Successfully processed %s -> %s\n", inputFile, outputFile)
}

// processMarkdown processes markdown content and embeds images as base64
func processMarkdown(content, baseDir string, debugMode bool) (string, error) {
	// Find all image references in the markdown (both markdown and HTML)
	imageRefs := findImageReferences(content)

	// Process each image reference
	result := content
	offset := 0 // Track position adjustments due to replacements

	for _, imgRef := range imageRefs {
		// Debug output
		if debugMode {
			log.Printf("Processing image: %s, Width: %d, Height: %d", imgRef.ImagePath, imgRef.Width, imgRef.Height)
		}

		// Determine the full path for the image
		var fullImagePath string
		if isURL(imgRef.ImagePath) {
			fullImagePath = imgRef.ImagePath
			// Debug URL if debug mode is enabled
			if debugMode {
				debugURL(fullImagePath)
			}
		} else {
			fullImagePath = filepath.Join(baseDir, imgRef.ImagePath)
		}

		// Convert image to base64
		base64Data, mimeType, err := imageToBase64(fullImagePath, imgRef.Width, imgRef.Height)
		if err != nil {
			log.Printf("Warning: Could not convert image %s to base64: %v", imgRef.ImagePath, err)
			continue
		}

		// Create the new base64 image reference
		var newImageRef string
		if imgRef.IsHTML {
			// Convert HTML img to markdown format
			newImageRef = fmt.Sprintf("![%s](data:%s;base64,%s)", imgRef.AltText, mimeType, base64Data)
		} else {
			// Keep markdown format, but remove size attributes since image is already resized
			newImageRef = fmt.Sprintf("![%s](data:%s;base64,%s)", imgRef.AltText, mimeType, base64Data)
		}

		// Replace the original reference
		adjustedStart := imgRef.StartPos + offset
		adjustedEnd := imgRef.EndPos + offset

		result = result[:adjustedStart] + newImageRef + result[adjustedEnd:]

		// Update offset for next replacements
		offset += len(newImageRef) - (imgRef.EndPos - imgRef.StartPos)
	}

	return result, nil
}

// findImageReferences finds all image references in markdown content (both markdown and HTML)
func findImageReferences(content string) []ImageReference {
	var refs []ImageReference

	// Find markdown image references: ![alt text](image_path){: width=X height=Y}
	markdownRegex := regexp.MustCompile(`!\[([^\]]*)\]\(([^=)]+)\)(\s*\{:\s*width=(\d+)\s*height=(\d+)\s*\})`)
	markdownMatches := markdownRegex.FindAllStringSubmatchIndex(content, -1)

	for _, match := range markdownMatches {
		fullMatch := content[match[0]:match[1]]
		altText := content[match[2]:match[3]]
		imagePath := content[match[4]:match[5]]

		// Skip if it's already a data URL
		if strings.HasPrefix(imagePath, "data:") {
			continue
		}

		// Parse size attributes if present
		width, height := 0, 0
		if len(match) > 8 && match[6] != -1 && match[7] != -1 {
			widthStr := content[match[8]:match[9]]
			heightStr := content[match[10]:match[11]]
			width, _ = strconv.Atoi(widthStr)
			height, _ = strconv.Atoi(heightStr)
		}

		refs = append(refs, ImageReference{
			FullMatch: fullMatch,
			AltText:   altText,
			ImagePath: imagePath,
			StartPos:  match[0],
			EndPos:    match[1],
			Width:     width,
			Height:    height,
			IsHTML:    false,
		})
	}

	// Find basic markdown image references: ![alt text](image_path)
	markdownBasicRegex := regexp.MustCompile(`!\[([^\]]*)\]\(([^=)]+)\)`)
	markdownBasicMatches := markdownBasicRegex.FindAllStringSubmatchIndex(content, -1)

	for _, match := range markdownBasicMatches {
		fullMatch := content[match[0]:match[1]]
		altText := content[match[2]:match[3]]
		imagePath := content[match[4]:match[5]]

		// Skip if it's already a data URL
		if strings.HasPrefix(imagePath, "data:") {
			continue
		}

		// Skip if this matches the new syntax (will be handled by the next regex)
		if strings.Contains(fullMatch, " =") {
			continue
		}

		// Skip if this has size attributes (will be handled by the first regex)
		// Check if there are size attributes after this match
		endPos := match[1]
		if endPos < len(content) {
			remainingContent := content[endPos:]
			if strings.HasPrefix(strings.TrimSpace(remainingContent), "{:") {
				continue
			}
		}

		refs = append(refs, ImageReference{
			FullMatch: fullMatch,
			AltText:   altText,
			ImagePath: imagePath,
			StartPos:  match[0],
			EndPos:    match[1],
			Width:     0,
			Height:    0,
			IsHTML:    false,
		})
	}

	// Find markdown image references with new syntax: ![alt text](image_path =WxH)
	markdownNewRegex := regexp.MustCompile(`!\[([^\]]*)\]\(([^=)]+?)\s*=\s*(\d+)x(\d+)\)`)
	markdownNewMatches := markdownNewRegex.FindAllStringSubmatchIndex(content, -1)

	for _, match := range markdownNewMatches {
		fullMatch := content[match[0]:match[1]]
		altText := content[match[2]:match[3]]
		imagePath := content[match[4]:match[5]]
		widthStr := content[match[6]:match[7]]
		heightStr := content[match[8]:match[9]]

		// Skip if it's already a data URL
		if strings.HasPrefix(imagePath, "data:") {
			continue
		}

		width, _ := strconv.Atoi(widthStr)
		height, _ := strconv.Atoi(heightStr)

		refs = append(refs, ImageReference{
			FullMatch: fullMatch,
			AltText:   altText,
			ImagePath: imagePath,
			StartPos:  match[0],
			EndPos:    match[1],
			Width:     width,
			Height:    height,
			IsHTML:    false,
		})
	}

	// Find HTML img tags: <img src="..." alt="..." width="..." height="...">
	htmlRegex := regexp.MustCompile(`<img[^>]+src=["']([^"']+)["'][^>]*alt=["']([^"']*)["'][^>]*width=["'](\d+)["'][^>]*height=["'](\d+)["'][^>]*>`)
	htmlMatches := htmlRegex.FindAllStringSubmatchIndex(content, -1)

	for _, match := range htmlMatches {
		fullMatch := content[match[0]:match[1]]
		imagePath := content[match[2]:match[3]]
		altText := content[match[4]:match[5]]
		widthStr := content[match[6]:match[7]]
		heightStr := content[match[8]:match[9]]

		// Skip if it's already a data URL
		if strings.HasPrefix(imagePath, "data:") {
			continue
		}

		width, _ := strconv.Atoi(widthStr)
		height, _ := strconv.Atoi(heightStr)

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

	// Also find HTML img tags without size attributes
	htmlSimpleRegex := regexp.MustCompile(`<img[^>]+src=["']([^"']+)["'][^>]*alt=["']([^"']*)["'][^>]*>`)
	htmlSimpleMatches := htmlSimpleRegex.FindAllStringSubmatchIndex(content, -1)

	for _, match := range htmlSimpleMatches {
		fullMatch := content[match[0]:match[1]]
		imagePath := content[match[2]:match[3]]
		altText := content[match[4]:match[5]]

		// Skip if it's already a data URL
		if strings.HasPrefix(imagePath, "data:") {
			continue
		}

		// Skip if we already processed this as a sized img tag
		alreadyProcessed := false
		for _, ref := range refs {
			if ref.StartPos == match[0] && ref.EndPos == match[1] {
				alreadyProcessed = true
				break
			}
		}
		if alreadyProcessed {
			continue
		}

		refs = append(refs, ImageReference{
			FullMatch: fullMatch,
			AltText:   altText,
			ImagePath: imagePath,
			StartPos:  match[0],
			EndPos:    match[1],
			Width:     0,
			Height:    0,
			IsHTML:    true,
		})
	}

	return refs
}

// ppmDecode decodes a PPM image
func ppmDecode(r io.Reader) (image.Image, error) {
	// PPM files start with "P6" for binary format
	// This is a simplified PPM decoder for P6 format (binary)

	// Read the magic number
	magic := make([]byte, 2)
	if _, err := io.ReadFull(r, magic); err != nil {
		return nil, fmt.Errorf("failed to read PPM magic number: %v", err)
	}

	if string(magic) != "P6" {
		return nil, fmt.Errorf("unsupported PPM format: %s", string(magic))
	}

	// Skip whitespace and comments
	var width, height, maxVal int
	var err error

	// Read dimensions and max value
	_, err = fmt.Fscanf(r, "%d %d %d", &width, &height, &maxVal)
	if err != nil {
		return nil, fmt.Errorf("failed to read PPM dimensions: %v", err)
	}

	// Skip any remaining whitespace and newline
	var c byte
	for {
		c, err = readByte(r)
		if err != nil {
			return nil, fmt.Errorf("failed to read PPM header: %v", err)
		}
		if c == '\n' {
			break
		}
	}

	// Create image
	img := image.NewRGBA(image.Rect(0, 0, width, height))

	// Read pixel data
	if maxVal <= 255 {
		// 8-bit per channel - read all pixels at once for efficiency
		pixelData := make([]byte, width*height*3)
		if _, err := io.ReadFull(r, pixelData); err != nil {
			return nil, fmt.Errorf("failed to read PPM pixel data: %v", err)
		}

		for y := 0; y < height; y++ {
			for x := 0; x < width; x++ {
				idx := (y*width + x) * 3
				img.Set(x, y, color.RGBA{pixelData[idx], pixelData[idx+1], pixelData[idx+2], 255})
			}
		}
	} else {
		// 16-bit per channel (big-endian) - read all pixels at once
		pixelData := make([]byte, width*height*6)
		if _, err := io.ReadFull(r, pixelData); err != nil {
			return nil, fmt.Errorf("failed to read PPM pixel data: %v", err)
		}

		for y := 0; y < height; y++ {
			for x := 0; x < width; x++ {
				idx := (y*width + x) * 6
				// Convert 16-bit to 8-bit
				r8 := uint8((uint16(pixelData[idx])<<8 | uint16(pixelData[idx+1])) >> 8)
				g8 := uint8((uint16(pixelData[idx+2])<<8 | uint16(pixelData[idx+3])) >> 8)
				b8 := uint8((uint16(pixelData[idx+4])<<8 | uint16(pixelData[idx+5])) >> 8)
				img.Set(x, y, color.RGBA{r8, g8, b8, 255})
			}
		}
	}

	return img, nil
}

func readByte(r io.Reader) (byte, error) {
	buf := make([]byte, 1)
	_, err := io.ReadFull(r, buf)
	return buf[0], err
}

// downloadAndEncodeExternalImage downloads an external image and encodes its raw binary data to base64
func downloadAndEncodeExternalImage(imageURL string, targetWidth, targetHeight int) (string, string, error) {
	// Create HTTP client with timeout
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	// Create request with proper headers
	req, err := http.NewRequest("GET", imageURL, nil)
	if err != nil {
		return "", "", fmt.Errorf("failed to create request: %v", err)
	}

	// Add headers to mimic a real browser request
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")
	req.Header.Set("Accept", "image/*,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	// Don't request compression for images to avoid encoding issues

	// Make the request
	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("failed to download image: %v", err)
	}
	defer resp.Body.Close()

	// Check if the request was successful
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("failed to download image: HTTP %d", resp.StatusCode)
	}

	// Read the entire response body (the raw image data)
	imageData, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", fmt.Errorf("failed to read image data: %v", err)
	}

	// Determine MIME type from Content-Type header
	mimeType := resp.Header.Get("Content-Type")
	if mimeType == "" {
		// Fallback: try to determine from URL extension
		if strings.HasSuffix(strings.ToLower(imageURL), ".png") {
			mimeType = "image/png"
		} else if strings.HasSuffix(strings.ToLower(imageURL), ".jpg") || strings.HasSuffix(strings.ToLower(imageURL), ".jpeg") {
			mimeType = "image/jpeg"
		} else if strings.HasSuffix(strings.ToLower(imageURL), ".gif") {
			mimeType = "image/gif"
		} else if strings.HasSuffix(strings.ToLower(imageURL), ".webp") {
			mimeType = "image/webp"
		} else if strings.HasSuffix(strings.ToLower(imageURL), ".svg") {
			mimeType = "image/svg+xml"
		} else {
			mimeType = "image/jpeg" // default fallback
		}
	}

	// Check if it's an SVG (either by MIME type or content)
	if mimeType == "image/svg+xml" || strings.Contains(strings.ToLower(string(imageData)), "<svg") {
		// For SVG, just encode the raw content as base64
		base64Data := base64.StdEncoding.EncodeToString(imageData)
		return base64Data, "image/svg+xml", nil
	}

	// If resizing is needed, decode the image, resize it, and re-encode
	if targetWidth > 0 && targetHeight > 0 {
		// Create a reader from the image data
		reader := bytes.NewReader(imageData)

		// Try different image formats to decode
		decoders := []struct {
			name   string
			decode func(io.Reader) (image.Image, error)
		}{
			{"PNG", png.Decode},
			{"JPEG", jpeg.Decode},
			{"GIF", gif.Decode},
			{"PPM", ppmDecode},
		}

		var img image.Image
		var format string

		for _, decoder := range decoders {
			// Reset reader
			reader.Seek(0, 0)

			// Try to decode
			if decodedImg, err := decoder.decode(reader); err == nil {
				img = decodedImg
				format = decoder.name
				break
			}
		}

		if img != nil {
			// Resize the image
			img = resizeImage(img, targetWidth, targetHeight)

			// Encode to appropriate format
			var encodeBuf bytes.Buffer
			var encoder func(io.Writer, image.Image) error

			// Determine MIME type and choose encoder
			switch strings.ToUpper(format) {
			case "PNG":
				mimeType = "image/png"
				encoder = png.Encode
			case "GIF":
				mimeType = "image/gif"
				encoder = func(w io.Writer, m image.Image) error {
					return gif.Encode(w, m, nil)
				}
			case "PPM":
				// Convert PPM to PNG for better compatibility
				mimeType = "image/png"
				encoder = png.Encode
			default:
				// For JPEG and other formats, use JPEG encoder
				mimeType = "image/jpeg"
				encoder = func(w io.Writer, m image.Image) error {
					return jpeg.Encode(w, m, &jpeg.Options{Quality: 85})
				}
			}

			// Encode the image
			if err := encoder(&encodeBuf, img); err != nil {
				return "", "", fmt.Errorf("failed to encode resized image: %v", err)
			}

			// Convert to base64
			base64Data := base64.StdEncoding.EncodeToString(encodeBuf.Bytes())
			return base64Data, mimeType, nil
		} else {
			// If decoding failed, try to determine the issue and provide better error handling
			// Check if the image data is valid by trying to detect the format
			if len(imageData) > 8 {
				// Check for PNG signature
				if imageData[0] == 0x89 && imageData[1] == 0x50 && imageData[2] == 0x4E && imageData[3] == 0x47 {
					// It's a PNG but failed to decode, return raw data
					base64Data := base64.StdEncoding.EncodeToString(imageData)
					return base64Data, "image/png", nil
				}
				// Check for JPEG signature
				if imageData[0] == 0xFF && imageData[1] == 0xD8 {
					// It's a JPEG but failed to decode, return raw data
					base64Data := base64.StdEncoding.EncodeToString(imageData)
					return base64Data, "image/jpeg", nil
				}
			}
			// If we can't determine the format, return raw data with original mime type
			base64Data := base64.StdEncoding.EncodeToString(imageData)
			return base64Data, mimeType, nil
		}
	}

	// If no resizing needed or decoding failed, encode the raw binary data to base64
	base64Data := base64.StdEncoding.EncodeToString(imageData)
	return base64Data, mimeType, nil
}

func imageToBase64(imagePath string, targetWidth, targetHeight int) (string, string, error) {
	// Check if it's a URL - handle external images differently
	if isURL(imagePath) {
		return downloadAndEncodeExternalImage(imagePath, targetWidth, targetHeight)
	}

	// For local images, use the existing logic
	var err error

	// Open the image file
	file, err := os.Open(imagePath)
	if err != nil {
		return "", "", fmt.Errorf("failed to open image file: %v", err)
	}
	defer file.Close()

	// Try different image formats
	decoders := []struct {
		name   string
		decode func(io.Reader) (image.Image, error)
	}{
		{"PNG", png.Decode},
		{"JPEG", jpeg.Decode},
		{"GIF", gif.Decode},
		{"PPM", ppmDecode},
	}

	var img image.Image
	var format string

	for _, decoder := range decoders {
		// Reset file pointer
		file.Seek(0, 0)

		// Try to decode
		if decodedImg, err := decoder.decode(file); err == nil {
			img = decodedImg
			format = decoder.name
			break
		}
	}

	// If no decoder worked, check if it's an SVG
	if img == nil {
		file.Seek(0, 0)
		svgBuf := make([]byte, 1024)
		n, err := file.Read(svgBuf)
		if err == nil {
			content := strings.ToLower(string(svgBuf[:n]))
			if strings.Contains(content, "<svg") {
				// Reset file pointer and handle SVG
				file.Seek(0, 0)
				return handleSVGImage(file, imagePath)
			}
		}
		return "", "", fmt.Errorf("unsupported image format")
	}

	// Resize image if needed
	if targetWidth > 0 && targetHeight > 0 {
		img = resizeImage(img, targetWidth, targetHeight)
	}

	// Encode to appropriate format
	var encodeBuf bytes.Buffer
	var encoder func(io.Writer, image.Image) error

	// Determine MIME type and choose encoder
	mimeType := "image/jpeg" // default
	switch strings.ToUpper(format) {
	case "PNG":
		mimeType = "image/png"
		encoder = png.Encode
	case "GIF":
		mimeType = "image/gif"
		encoder = func(w io.Writer, m image.Image) error {
			return gif.Encode(w, m, nil)
		}
	case "PPM":
		// Convert PPM to PNG for better compatibility
		mimeType = "image/png"
		encoder = png.Encode
	default:
		// For JPEG and other formats, use JPEG encoder
		mimeType = "image/jpeg"
		encoder = func(w io.Writer, m image.Image) error {
			return jpeg.Encode(w, m, &jpeg.Options{Quality: 85})
		}
	}

	// Encode the image
	if err := encoder(&encodeBuf, img); err != nil {
		return "", "", fmt.Errorf("failed to encode image: %v", err)
	}

	// Convert to base64
	base64Data := base64.StdEncoding.EncodeToString(encodeBuf.Bytes())
	return base64Data, mimeType, nil
}

// handleSVGImage handles SVG images by reading them as text and encoding as base64
func handleSVGImage(file *os.File, imagePath string) (string, string, error) {
	// Read the entire SVG content
	content, err := io.ReadAll(file)
	if err != nil {
		return "", "", fmt.Errorf("failed to read SVG file: %v", err)
	}

	// Encode SVG content as base64
	base64Data := base64.StdEncoding.EncodeToString(content)
	return base64Data, "image/svg+xml", nil
}

// isURL checks if the given string is a valid URL
func isURL(str string) bool {
	_, err := url.Parse(str)
	return err == nil && (strings.HasPrefix(str, "http://") || strings.HasPrefix(str, "https://"))
}

// debugURL downloads a small portion of a URL to see what it returns
func debugURL(url string) {
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		log.Printf("Debug: Failed to create request for %s: %v", url, err)
		return
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")

	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Debug: Failed to download %s: %v", url, err)
		return
	}
	defer resp.Body.Close()

	log.Printf("Debug: %s - Status: %d, Content-Type: %s, Content-Length: %d",
		url, resp.StatusCode, resp.Header.Get("Content-Type"), resp.ContentLength)

	// Read first 500 bytes to see what we're getting
	buf := make([]byte, 500)
	n, err := resp.Body.Read(buf)
	if err != nil && err != io.EOF {
		log.Printf("Debug: Failed to read response body: %v", err)
		return
	}

	content := string(buf[:n])
	if len(content) > 200 {
		content = content[:200] + "..."
	}
	log.Printf("Debug: %s - First 200 chars: %s", url, content)
}

// downloadImage downloads an image from a URL and saves it to a temporary file
func downloadImage(imageURL string) (*os.File, error) {
	// Create HTTP client with timeout
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	// Create request with proper headers
	req, err := http.NewRequest("GET", imageURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}

	// Add headers to mimic a real browser request
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")
	req.Header.Set("Accept", "image/webp,image/apng,image/*,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Upgrade-Insecure-Requests", "1")

	// Make the request
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to download image: %v", err)
	}
	defer resp.Body.Close()

	// Check if the response is successful
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP error: %d", resp.StatusCode)
	}

	// Check content type to ensure it's an image
	contentType := resp.Header.Get("Content-Type")
	log.Printf("Debug: %s - Content-Type: %s, Content-Length: %d", imageURL, contentType, resp.ContentLength)

	if !strings.HasPrefix(contentType, "image/") {
		return nil, fmt.Errorf("not an image: content-type is %s", contentType)
	}

	// Create a temporary file
	tempFile, err := os.CreateTemp("", "image_*.tmp")
	if err != nil {
		return nil, fmt.Errorf("failed to create temporary file: %v", err)
	}

	// Copy the response body to the temporary file
	bytesWritten, err := io.Copy(tempFile, resp.Body)
	if err != nil {
		tempFile.Close()
		os.Remove(tempFile.Name())
		return nil, fmt.Errorf("failed to save image to temporary file: %v", err)
	}

	log.Printf("Debug: %s - Wrote %d bytes to %s", imageURL, bytesWritten, tempFile.Name())

	// Seek to the beginning of the file
	_, err = tempFile.Seek(0, 0)
	if err != nil {
		tempFile.Close()
		os.Remove(tempFile.Name())
		return nil, fmt.Errorf("failed to seek to beginning of file: %v", err)
	}

	return tempFile, nil
}

// validateImageFile checks if a file is a valid image by attempting to decode it
func validateImageFile(filePath string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	// Check if file is HTML (error page)
	buf := make([]byte, 1024) // Increased buffer size for better detection
	n, err := file.Read(buf)
	if err != nil && err != io.EOF {
		return err
	}

	content := strings.ToLower(string(buf[:n]))

	// Check for various HTML indicators
	if strings.Contains(content, "<html") ||
		strings.Contains(content, "<!doctype") ||
		strings.Contains(content, "<head") ||
		strings.Contains(content, "<body") ||
		strings.Contains(content, "error") ||
		strings.Contains(content, "not found") ||
		strings.Contains(content, "forbidden") ||
		strings.Contains(content, "access denied") {
		return fmt.Errorf("downloaded file appears to be an HTML page (likely an error page), not an image")
	}

	// Check for SVG content
	if strings.Contains(content, "<?xml") && strings.Contains(content, "<svg") {
		// This is an SVG file, which is valid
		return nil
	}

	// Check for common image file signatures
	if strings.HasPrefix(content, "\x89png\r\n\x1a\n") || // PNG
		strings.HasPrefix(content, "\xff\xd8\xff") || // JPEG
		strings.HasPrefix(content, "gif87a") || // GIF87a
		strings.HasPrefix(content, "gif89a") { // GIF89a
		// File has valid image signature, skip strict validation
		return nil
	}

	// Reset file pointer and validate as image
	file.Seek(0, 0)
	_, _, err = image.DecodeConfig(file)
	if err != nil {
		// Try to provide more specific error information
		if strings.Contains(err.Error(), "unknown format") {
			return fmt.Errorf("image format not recognized - file may be corrupted or not an image")
		}
		return err
	}

	return nil
}

// resizeImage resizes an image to the specified dimensions
func resizeImage(img image.Image, targetWidth, targetHeight int) image.Image {
	bounds := img.Bounds()
	srcWidth := bounds.Dx()
	srcHeight := bounds.Dy()

	// If no dimensions specified, check if image is larger than 200px
	if targetWidth <= 0 && targetHeight <= 0 {
		if srcWidth > 200 || srcHeight > 200 {
			// Calculate new dimensions maintaining aspect ratio with max 200px
			if srcWidth > srcHeight {
				targetWidth = 200
				targetHeight = int(float64(200) * float64(srcHeight) / float64(srcWidth))
			} else {
				targetHeight = 200
				targetWidth = int(float64(200) * float64(srcWidth) / float64(srcHeight))
			}
		} else {
			// Image is already small enough, return original
			return img
		}
	} else {
		// If only one dimension is specified, calculate the other to maintain aspect ratio
		if targetWidth > 0 && targetHeight == 0 {
			targetHeight = int(float64(targetWidth) * float64(srcHeight) / float64(srcWidth))
		} else if targetHeight > 0 && targetWidth == 0 {
			targetWidth = int(float64(targetHeight) * float64(srcWidth) / float64(srcHeight))
		}
		// Note: We don't limit user-specified dimensions to 200px
		// The user knows what they want, so we respect their choice
	}

	// Create new image with target dimensions
	resized := image.NewRGBA(image.Rect(0, 0, targetWidth, targetHeight))

	// Use high-quality scaling
	draw.ApproxBiLinear.Scale(resized, resized.Bounds(), img, img.Bounds(), draw.Over, nil)

	return resized
}

// getMimeType determines the MIME type based on file extension
func getMimeType(filePath string) string {
	ext := strings.ToLower(filepath.Ext(filePath))

	switch ext {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".svg":
		return "image/svg+xml"
	case ".webp":
		return "image/webp"
	case ".bmp":
		return "image/bmp"
	case ".ico":
		return "image/x-icon"
	default:
		return "image/jpeg" // Default fallback
	}
}
