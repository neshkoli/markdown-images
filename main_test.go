package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupTestServer creates a mock HTTP server for testing remote image downloads.
func setupTestServer() (*httptest.Server, []byte) {
	// Create a new 1x1 RGBA image
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.Black) // Set pixel to black

	// Encode the image to JPEG format
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		// This should not fail for a simple image, but handle it just in case
		panic(fmt.Sprintf("Failed to encode test JPEG: %v", err))
	}
	jpegData := buf.Bytes()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".jpg") {
			w.Header().Set("Content-Type", "image/jpeg")
			w.Write(jpegData)
		} else if strings.HasSuffix(r.URL.Path, ".svg") {
			w.Header().Set("Content-Type", "image/svg+xml")
			fmt.Fprint(w, `<svg width="100" height="100"><circle cx="50" cy="50" r="40" stroke="green" stroke-width="4" fill="yellow" /></svg>`)
		} else {
			http.NotFound(w, r)
		}
	}))
	return server, jpegData
}

func TestProcessMarkdown(t *testing.T) {
	// Setup mock server
	server, jpegData := setupTestServer()
	defer server.Close()

	// Create a dummy image file in a temporary directory
	tempDir := t.TempDir()
	dummyImagePath := filepath.Join(tempDir, "test.jpg")
	err := os.WriteFile(dummyImagePath, jpegData, 0644)
	if err != nil {
		t.Fatalf("Failed to create dummy image file: %v", err)
	}

	testCases := []struct {
		name          string
		markdown      string
		expectedCount int
		baseDir       string
	}{
		{
			name:          "Local image with no dimensions",
			markdown:      "![test image](test.jpg)",
			expectedCount: 1,
			baseDir:       tempDir,
		},
		{
			name:          "Local image with width only",
			markdown:      "![test image](test.jpg){: width=100}",
			expectedCount: 1,
			baseDir:       tempDir,
		},
		{
			name:          "Local image with height only",
			markdown:      "![test image](test.jpg){: height=100}",
			expectedCount: 1,
			baseDir:       tempDir,
		},
		{
			name:          "Local image with both dimensions",
			markdown:      "![test image](test.jpg){: width=100 height=50}",
			expectedCount: 1,
			baseDir:       tempDir,
		},
		{
			name:          "Remote image with no dimensions",
			markdown:      fmt.Sprintf("![remote image](%s/test.jpg)", server.URL),
			expectedCount: 1,
			baseDir:       tempDir, // baseDir is not used for remote images
		},
		{
			name:          "HTML image tag with local file",
			markdown:      `<img src="test.jpg" alt="html test">`,
			expectedCount: 1,
			baseDir:       tempDir,
		},
		{
			name:          "HTML image tag with remote file",
			markdown:      fmt.Sprintf(`<img src="%s/test.jpg" alt="html remote test">`, server.URL),
			expectedCount: 1,
			baseDir:       tempDir,
		},
		{
			name:          "Multiple images",
			markdown:      fmt.Sprintf("![local](test.jpg) ![remote](%s/test.jpg)", server.URL),
			expectedCount: 2,
			baseDir:       tempDir,
		},
		{
			name:          "Image not found",
			markdown:      "![not found](nonexistent.jpg)",
			expectedCount: 0, // Expect 0 successful embeddings
			baseDir:       tempDir,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			processed, err := processMarkdown(tc.markdown, tc.baseDir, false)
			if err != nil {
				if tc.name != "Image not found" {
					t.Errorf("processMarkdown failed: %v", err)
				}
			}

			count := strings.Count(processed, "data:image/jpeg;base64,")
			if count != tc.expectedCount {
				t.Errorf("Expected %d embedded images, but got %d", tc.expectedCount, count)
				t.Logf("Processed markdown: %s", processed)
			}

			if tc.expectedCount == 0 && !strings.Contains(processed, "![not found](nonexistent.jpg)") {
				t.Errorf("Expected original tag to be present on failure, but it was not.")
			}
		})
	}
}
