package markdown_test

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"markdown-images/markdown"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// setupTestServer creates a mock HTTP server for testing remote image downloads.
func setupTestServer() (*httptest.Server, []byte, []byte) {
	// Create a new 1x1 RGBA image for JPEG
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.Black) // Set pixel to black

	// Encode the image to JPEG format
	var jpegBuf bytes.Buffer
	if err := jpeg.Encode(&jpegBuf, img, nil); err != nil {
		panic(fmt.Sprintf("Failed to encode test JPEG: %v", err))
	}
	jpegData := jpegBuf.Bytes()

	// Create a new 1x1 RGBA image for PNG
	pngImg := image.NewRGBA(image.Rect(0, 0, 1, 1))
	pngImg.Set(0, 0, color.RGBA{255, 0, 0, 255}) // Use a different color for PNG

	// Encode the image to PNG format
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, pngImg); err != nil {
		panic(fmt.Sprintf("Failed to encode test PNG: %v", err))
	}
	pngData := pngBuf.Bytes()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".jpg") {
			w.Header().Set("Content-Type", "image/jpeg")
			w.Write(jpegData)
		} else if strings.HasSuffix(r.URL.Path, ".svg") {
			w.Header().Set("Content-Type", "image/svg+xml")
			fmt.Fprint(w, `<svg width="100" height="100"><circle cx="50" cy="50" r="40" stroke="green" stroke-width="4" fill="yellow" /></svg>`)
		} else if strings.HasSuffix(r.URL.Path, ".png") {
			w.Header().Set("Content-Type", "image/png")
			w.Write(pngData)
		} else {
			http.NotFound(w, r)
		}
	}))
	return server, jpegData, pngData
}

func TestProcessMarkdown(t *testing.T) {
	// Setup mock server
	server, jpegData, pngData := setupTestServer()
	defer server.Close()

	// Create a dummy JPEG image file in a temporary directory
	tempDir := t.TempDir()
	dummyJPEGPath := filepath.Join(tempDir, "test.jpg")
	err := os.WriteFile(dummyJPEGPath, jpegData, 0644)
	if err != nil {
		t.Fatalf("Failed to create dummy JPEG image file: %v", err)
	}

	// Create a dummy PNG image file
	dummyPNGPath := filepath.Join(tempDir, "test.png")
	err = os.WriteFile(dummyPNGPath, pngData, 0644)
	if err != nil {
		t.Fatalf("Failed to create dummy PNG file: %v", err)
	}

	// Create a dummy SVG file
	dummySVGPath := filepath.Join(tempDir, "test.svg")
	svgContent := []byte(`<svg width="100" height="100"><circle cx="50" cy="50" r="40" stroke="green" stroke-width="4" fill="yellow" /></svg>`)
	err = os.WriteFile(dummySVGPath, svgContent, 0644)
	if err != nil {
		t.Fatalf("Failed to create dummy SVG file: %v", err)
	}

	testCases := []struct {
		name          string
		markdown      string
		expectedCount int
		baseDir       string
		expectedMime  string // "image/jpeg", "image/png", or "image/svg+xml"
		checkResize   bool
		expectedWidth int
	}{
		{
			name:          "Local image with no dimensions",
			markdown:      "![test image](test.jpg)",
			expectedCount: 1,
			baseDir:       tempDir,
			expectedMime:  "image/jpeg",
		},
		{
			name:          "Local image with width only",
			markdown:      "![test image](test.jpg){: width=100}",
			expectedCount: 1,
			baseDir:       tempDir,
			expectedMime:  "image/jpeg",
		},
		{
			name:          "Local image with height only",
			markdown:      "![test image](test.jpg){: height=100}",
			expectedCount: 1,
			baseDir:       tempDir,
			expectedMime:  "image/jpeg",
		},
		{
			name:          "Local image with both dimensions",
			markdown:      "![test image](test.jpg){: width=100 height=50}",
			expectedCount: 1,
			baseDir:       tempDir,
			expectedMime:  "image/jpeg",
		},
		{
			name:          "Remote image with no dimensions",
			markdown:      fmt.Sprintf("![remote image](%s/test.jpg)", server.URL),
			expectedCount: 1,
			baseDir:       tempDir, // baseDir is not used for remote images
			expectedMime:  "image/jpeg",
		},
		{
			name:          "HTML image tag with local file",
			markdown:      `<img src="test.jpg" alt="html test">`,
			expectedCount: 1,
			baseDir:       tempDir,
			expectedMime:  "image/jpeg",
		},
		{
			name:          "HTML image tag with remote file",
			markdown:      fmt.Sprintf(`<img src="%s/test.jpg" alt="html remote test">`, server.URL),
			expectedCount: 1,
			baseDir:       tempDir,
			expectedMime:  "image/jpeg",
		},
		{
			name:          "Multiple images",
			markdown:      fmt.Sprintf("![local](test.jpg) ![remote](%s/test.jpg)", server.URL),
			expectedCount: 2,
			baseDir:       tempDir,
			expectedMime:  "image/jpeg",
		},
		{
			name:          "Image not found",
			markdown:      "![not found](nonexistent.jpg)",
			expectedCount: 0, // Expect 0 successful embeddings
			baseDir:       tempDir,
			expectedMime:  "image/jpeg",
		},
		{
			name:          "Local SVG image",
			markdown:      "![test svg](test.svg)",
			expectedCount: 1,
			baseDir:       tempDir,
			expectedMime:  "image/svg+xml",
		},
		{
			name:          "Remote SVG image",
			markdown:      fmt.Sprintf("![remote svg](%s/test.svg)", server.URL),
			expectedCount: 1,
			baseDir:       tempDir,
			expectedMime:  "image/svg+xml",
		},
		{
			name:          "Local SVG with resize",
			markdown:      "![test svg](test.svg){: width=200}",
			expectedCount: 1,
			baseDir:       tempDir,
			expectedMime:  "image/svg+xml",
			checkResize:   true,
			expectedWidth: 200,
		},
		{
			name:          "HTML image with local SVG and resize",
			markdown:      `<img src="test.svg" alt="html test" width="300">`,
			expectedCount: 1,
			baseDir:       tempDir,
			expectedMime:  "image/svg+xml",
			checkResize:   true,
			expectedWidth: 300,
		},
		{
			name:          "Local PNG image",
			markdown:      "![test png](test.png)",
			expectedCount: 1,
			baseDir:       tempDir,
			expectedMime:  "image/png",
		},
		{
			name:          "Remote PNG image",
			markdown:      fmt.Sprintf("![remote png](%s/test.png)", server.URL),
			expectedCount: 1,
			baseDir:       tempDir,
			expectedMime:  "image/png",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			processed, err := markdown.ProcessMarkdown(tc.markdown, tc.baseDir, false)
			if err != nil {
				// We expect an error for the "Image not found" case, so don't fail the test for it.
				// For other cases, it's an unexpected error.
				if tc.name != "Image not found" {
					t.Errorf("ProcessMarkdown failed: %v", err)
				}
			}

			mimeTypeString := fmt.Sprintf("data:%s;base64,", tc.expectedMime)
			count := strings.Count(processed, mimeTypeString)
			if count != tc.expectedCount {
				t.Errorf("Expected %d embedded images of type %s, but got %d", tc.expectedCount, tc.expectedMime, count)
				t.Logf("Processed markdown: %s", processed)
			}

			if tc.checkResize {
				expectedWidthStr := fmt.Sprintf(`width="%d"`, tc.expectedWidth)
				// We need to decode the base64 string to check the content
				re := regexp.MustCompile(fmt.Sprintf("%s([^)]+)", regexp.QuoteMeta(mimeTypeString)))
				matches := re.FindStringSubmatch(processed)
				if len(matches) > 1 {
					base64Data := matches[1]
					decoded, err := base64.StdEncoding.DecodeString(base64Data)
					if err != nil {
						t.Fatalf("Failed to decode base64 string: %v", err)
					}
					if !strings.Contains(string(decoded), expectedWidthStr) {
						t.Errorf("Expected resized SVG to have width %s, but it was not found", expectedWidthStr)
						t.Logf("Decoded SVG: %s", string(decoded))
					}
				} else {
					t.Errorf("Could not find base64 data in processed markdown")
				}
			}

			// If we expected 0 images, the original markdown for the missing image should still be there.
			if tc.expectedCount == 0 && !strings.Contains(processed, "![not found](nonexistent.jpg)") {
				t.Errorf("Expected original tag to be present on failure, but it was not.")
			}
		})
	}
}
