package media

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDetectBase64Images verifies Base64 image detection
func TestDetectBase64Images(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		expected bool
	}{
		{
			name:     "contains Base64 PNG",
			body:     `{"image": "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAACklEQVR4nGMAAQAABQABDQottAAAAABJRU5ErkJggg=="}`,
			expected: true,
		},
		{
			name:     "contains Base64 JPEG",
			body:     `{"photo": "data:image/jpeg;base64,/9j/4AAQSkZJRgABAQEAYABgAAD"}`,
			expected: true,
		},
		{
			name:     "no Base64 image",
			body:     `{"text": "hello world"}`,
			expected: false,
		},
		{
			name:     "empty body",
			body:     "",
			expected: false,
		},
		{
			name:     "regular base64 but not image",
			body:     `{"data": "SGVsbG8gV29ybGQ="}`,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := DetectBase64Images(tt.body)
			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}

// TestEstimateBase64ImageSize verifies size estimation
func TestEstimateBase64ImageSize(t *testing.T) {
	// Create a small PNG (1x1 pixel)
	smallPNG := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAACklEQVR4nGMAAQAABQABDQottAAAAABJRU5ErkJggg=="
	body := `{"image": "data:image/png;base64,` + smallPNG + `"}`

	sizeKB := EstimateBase64ImageSize(body)

	// The size should be less than 1KB
	if sizeKB >= 1 {
		t.Errorf("Expected size < 1KB for small image, got %d KB", sizeKB)
	}

	// Test with larger content (repeated data)
	largeData := strings.Repeat("A", 150000) // ~150KB Base64
	largeBody := `{"image": "data:image/png;base64,` + largeData + `"}`

	largeSizeKB := EstimateBase64ImageSize(largeBody)
	if largeSizeKB < 100 {
		t.Errorf("Expected size > 100KB for large image, got %d KB", largeSizeKB)
	}
}

// TestExtractFromBody_SmallImage verifies small images remain inline
func TestExtractFromBody_SmallImage(t *testing.T) {
	tempDir := t.TempDir()
	extractor := NewExtractor(true, 100, tempDir) // 100KB minimum

	// Create a small PNG (< 100KB)
	smallPNG := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAACklEQVR4nGMAAQAABQABDQottAAAAABJRU5ErkJggg=="
	body := `{"image": "data:image/png;base64,` + smallPNG + `"}`

	modifiedBody, refs, err := extractor.ExtractFromBody(body, 0, "request")

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Small image should remain inline
	if len(refs) != 0 {
		t.Error("Expected no references for small image")
	}

	if modifiedBody != body {
		t.Error("Body should remain unchanged for small image")
	}
}

// TestExtractFromBody_LargeImage verifies large images are extracted
func TestExtractFromBody_LargeImage(t *testing.T) {
	tempDir := t.TempDir()
	extractor := NewExtractor(true, 10, tempDir) // 10KB minimum for testing

	// Create a large Base64 image (> 10KB)
	// Generate ~15KB of Base64 data
	largeData := strings.Repeat("ABCD", 5000) // 20,000 chars = ~15KB decoded
	body := `{"image": "data:image/png;base64,` + largeData + `"}`

	modifiedBody, refs, err := extractor.ExtractFromBody(body, 123, "request")

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Large image should be extracted
	if len(refs) != 1 {
		t.Fatalf("Expected 1 reference, got %d", len(refs))
	}

	// Check reference details
	ref := refs[0]
	if ref.Type != "image/png" {
		t.Errorf("Expected type 'image/png', got '%s'", ref.Type)
	}

	if ref.Placeholder != "[IMAGE_EXTRACTED:0]" {
		t.Errorf("Expected placeholder '[IMAGE_EXTRACTED:0]', got '%s'", ref.Placeholder)
	}

	if ref.SHA256 == "" {
		t.Error("SHA256 hash should not be empty")
	}

	if ref.SizeBytes <= 0 {
		t.Error("Size should be positive")
	}

	// Body should contain placeholder instead of image data
	if !strings.Contains(modifiedBody, "[IMAGE_EXTRACTED:0]") {
		t.Error("Modified body should contain placeholder")
	}

	if strings.Contains(modifiedBody, largeData) {
		t.Error("Modified body should not contain original image data")
	}

	// Verify file was created
	if ref.FilePath == "" {
		t.Fatal("FilePath should not be empty")
	}

	fullPath := filepath.Join(tempDir, ref.FilePath)
	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		t.Errorf("Expected file to exist at %s", fullPath)
	}
}

// TestExtractFromBody_MultipleImages verifies multiple images extraction
func TestExtractFromBody_MultipleImages(t *testing.T) {
	tempDir := t.TempDir()
	extractor := NewExtractor(true, 10, tempDir)

	// Create body with multiple large images
	largeData1 := strings.Repeat("ABCD", 5000)
	largeData2 := strings.Repeat("EFGH", 5000)
	body := `{
		"image1": "data:image/png;base64,` + largeData1 + `",
		"image2": "data:image/jpeg;base64,` + largeData2 + `"
	}`

	modifiedBody, refs, err := extractor.ExtractFromBody(body, 456, "response")

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Both images should be extracted
	if len(refs) != 2 {
		t.Fatalf("Expected 2 references, got %d", len(refs))
	}

	// Verify placeholders
	if !strings.Contains(modifiedBody, "[IMAGE_EXTRACTED:0]") {
		t.Error("Missing placeholder for first image")
	}

	if !strings.Contains(modifiedBody, "[IMAGE_EXTRACTED:1]") {
		t.Error("Missing placeholder for second image")
	}

	// Verify types
	if refs[0].Type != "image/png" {
		t.Errorf("Expected first image to be PNG, got %s", refs[0].Type)
	}

	if refs[1].Type != "image/jpeg" {
		t.Errorf("Expected second image to be JPEG, got %s", refs[1].Type)
	}

	// Verify both files exist
	for i, ref := range refs {
		fullPath := filepath.Join(tempDir, ref.FilePath)
		if _, err := os.Stat(fullPath); os.IsNotExist(err) {
			t.Errorf("Expected file %d to exist at %s", i, fullPath)
		}
	}
}

// TestExtractFromBody_Disabled verifies extraction is disabled when configured
func TestExtractFromBody_Disabled(t *testing.T) {
	tempDir := t.TempDir()
	extractor := NewExtractor(false, 10, tempDir) // Disabled

	largeData := strings.Repeat("ABCD", 5000)
	body := `{"image": "data:image/png;base64,` + largeData + `"}`

	modifiedBody, refs, err := extractor.ExtractFromBody(body, 0, "request")

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// No extraction should occur
	if len(refs) != 0 {
		t.Error("Expected no references when extraction is disabled")
	}

	if modifiedBody != body {
		t.Error("Body should remain unchanged when extraction is disabled")
	}
}

// TestExtractFromBody_InvalidBase64 verifies invalid Base64 is skipped
func TestExtractFromBody_InvalidBase64(t *testing.T) {
	tempDir := t.TempDir()
	extractor := NewExtractor(true, 10, tempDir)

	// Invalid Base64 (contains invalid characters)
	invalidData := strings.Repeat("!!!invalid!!!", 5000)
	body := `{"image": "data:image/png;base64,` + invalidData + `"}`

	modifiedBody, refs, err := extractor.ExtractFromBody(body, 0, "request")

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Invalid Base64 should be skipped
	if len(refs) != 0 {
		t.Error("Expected no references for invalid Base64")
	}

	if modifiedBody != body {
		t.Error("Body should remain unchanged for invalid Base64")
	}
}

// TestExtractFromBody_MixedSizes verifies mixed small and large images
func TestExtractFromBody_MixedSizes(t *testing.T) {
	tempDir := t.TempDir()
	extractor := NewExtractor(true, 10, tempDir)

	// Small image (< 10KB)
	smallPNG := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAACklEQVR4nGMAAQAABQABDQottAAAAABJRU5ErkJggg=="
	// Large image (> 10KB)
	largeData := strings.Repeat("ABCD", 5000)

	body := `{
		"small": "data:image/png;base64,` + smallPNG + `",
		"large": "data:image/png;base64,` + largeData + `"
	}`

	modifiedBody, refs, err := extractor.ExtractFromBody(body, 0, "request")

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Only large image should be extracted
	if len(refs) != 1 {
		t.Fatalf("Expected 1 reference (large image only), got %d", len(refs))
	}

	// Small image should remain inline
	if !strings.Contains(modifiedBody, smallPNG) {
		t.Error("Small image should remain in body")
	}

	// Large image should be replaced with placeholder
	if !strings.Contains(modifiedBody, "[IMAGE_EXTRACTED:0]") {
		t.Error("Large image should be replaced with placeholder")
	}
}

// TestSaveMedia_FileCreation verifies file is created correctly
func TestSaveMedia_FileCreation(t *testing.T) {
	tempDir := t.TempDir()
	extractor := NewExtractor(true, 10, tempDir)

	// Test data
	testData := []byte("test image data")
	sequenceID := uint64(789)

	filePath, err := extractor.saveMedia(testData, sequenceID, "request", 0, "png")

	if err != nil {
		t.Fatalf("Failed to save media: %v", err)
	}

	// Verify file path format
	if !strings.Contains(filePath, "seq_789_request_0.png") {
		t.Errorf("Expected filename pattern 'seq_789_request_0.png', got '%s'", filePath)
	}

	// Verify file exists and has correct content
	fullPath := filepath.Join(tempDir, filePath)
	content, err := os.ReadFile(fullPath)
	if err != nil {
		t.Fatalf("Failed to read saved file: %v", err)
	}

	if string(content) != string(testData) {
		t.Error("Saved file content doesn't match original data")
	}
}

// TestSaveMedia_DirectoryCreation verifies directory is created if missing
func TestSaveMedia_DirectoryCreation(t *testing.T) {
	tempDir := t.TempDir()
	// Use non-existent subdirectory
	storageDir := filepath.Join(tempDir, "media")
	extractor := NewExtractor(true, 10, storageDir)

	testData := []byte("test")
	_, err := extractor.saveMedia(testData, 0, "request", 0, "png")

	if err != nil {
		t.Fatalf("Failed to save media: %v", err)
	}

	// Verify directory was created
	if _, err := os.Stat(storageDir); os.IsNotExist(err) {
		t.Error("Expected storage directory to be created")
	}
}

// TestNewExtractor verifies extractor initialization
func TestNewExtractor(t *testing.T) {
	extractor := NewExtractor(true, 50, "/tmp/media")

	if !extractor.enabled {
		t.Error("Expected enabled to be true")
	}

	if extractor.minSizeKB != 50 {
		t.Errorf("Expected minSizeKB to be 50, got %d", extractor.minSizeKB)
	}

	if extractor.storagePath != "/tmp/media" {
		t.Errorf("Expected storagePath to be '/tmp/media', got '%s'", extractor.storagePath)
	}
}

// TestExtractFromBody_SHA256Calculation verifies SHA256 hash is computed correctly
func TestExtractFromBody_SHA256Calculation(t *testing.T) {
	tempDir := t.TempDir()
	extractor := NewExtractor(true, 10, tempDir)

	// Known data for hash verification
	data := strings.Repeat("TEST", 5000)
	body := `{"image": "data:image/png;base64,` + data + `"}`

	_, refs, err := extractor.ExtractFromBody(body, 0, "request")

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if len(refs) != 1 {
		t.Fatalf("Expected 1 reference, got %d", len(refs))
	}

	// SHA256 should be 64 hex characters
	if len(refs[0].SHA256) != 64 {
		t.Errorf("Expected SHA256 to be 64 characters, got %d", len(refs[0].SHA256))
	}

	// Should be valid hex
	for _, c := range refs[0].SHA256 {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("SHA256 contains invalid hex character: %c", c)
		}
	}
}

// BenchmarkExtractFromBody benchmarks media extraction performance
func BenchmarkExtractFromBody(b *testing.B) {
	tempDir := b.TempDir()
	extractor := NewExtractor(true, 10, tempDir)

	// Create realistic body with large image
	largeData := strings.Repeat("ABCD", 10000) // ~40KB
	body := `{"image": "data:image/png;base64,` + largeData + `"}`

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _ = extractor.ExtractFromBody(body, uint64(i), "request")
	}
}

// TestExtractFromBody_RealBase64PNG verifies extraction with real PNG data
func TestExtractFromBody_RealBase64PNG(t *testing.T) {
	tempDir := t.TempDir()
	extractor := NewExtractor(true, 0, tempDir) // 0KB minimum for testing

	// Real 1x1 pixel PNG (decoded is 95 bytes)
	realPNG := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="
	body := `{"image": "data:image/png;base64,` + realPNG + `"}`

	_, refs, err := extractor.ExtractFromBody(body, 0, "request")

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if len(refs) != 1 {
		t.Fatalf("Expected 1 reference, got %d", len(refs))
	}

	// Verify the decoded file is valid
	fullPath := filepath.Join(tempDir, refs[0].FilePath)
	decoded, err := os.ReadFile(fullPath)
	if err != nil {
		t.Fatalf("Failed to read extracted file: %v", err)
	}

	// PNG files start with specific magic bytes
	if len(decoded) < 8 {
		t.Fatal("Decoded file too small to be a PNG")
	}

	// Check PNG magic bytes: 89 50 4E 47 0D 0A 1A 0A
	expected := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	for i, b := range expected {
		if decoded[i] != b {
			t.Errorf("Invalid PNG magic byte at position %d: expected %x, got %x", i, b, decoded[i])
		}
	}
}
