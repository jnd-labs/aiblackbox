package media

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jnd-labs/aiblackbox/internal/models"
)

// Base64 image pattern: data:image/{type};base64,{data}
var base64ImagePattern = regexp.MustCompile(`data:image/(png|jpeg|jpg|gif|webp|bmp);base64,([A-Za-z0-9+/=]+)`)

// Extractor handles extraction of large Base64-encoded media to separate files
type Extractor struct {
	enabled     bool
	minSizeKB   int64
	storagePath string
}

// NewExtractor creates a new media extractor
func NewExtractor(enabled bool, minSizeKB int64, storagePath string) *Extractor {
	return &Extractor{
		enabled:     enabled,
		minSizeKB:   minSizeKB,
		storagePath: storagePath,
	}
}

// ExtractFromBody extracts large Base64 images from request/response body
// Returns the modified body with placeholders and list of media references
func (e *Extractor) ExtractFromBody(body string, sequenceID uint64, bodyType string) (string, []models.MediaReference, error) {
	if !e.enabled || body == "" {
		return body, nil, nil
	}

	var references []models.MediaReference
	modifiedBody := body
	index := 0

	// Find all Base64 image matches
	matches := base64ImagePattern.FindAllStringSubmatch(body, -1)

	for _, match := range matches {
		if len(match) < 3 {
			continue
		}

		fullMatch := match[0]      // data:image/png;base64,iVBOR...
		imageType := match[1]      // png, jpeg, etc.
		base64Data := match[2]     // The actual Base64 data

		// Check if the image is large enough to extract
		decodedSize := (len(base64Data) * 3) / 4 // Approximate decoded size
		if int64(decodedSize)/1024 < e.minSizeKB {
			continue // Too small, leave inline
		}

		// Decode the Base64 data
		decoded, err := base64.StdEncoding.DecodeString(base64Data)
		if err != nil {
			// Invalid Base64, skip this one
			continue
		}

		// Compute SHA256 hash of the original Base64 content
		hash := sha256.Sum256([]byte(base64Data))
		hashStr := hex.EncodeToString(hash[:])

		// Create placeholder
		placeholder := fmt.Sprintf("[IMAGE_EXTRACTED:%d]", index)

		// Save the file
		filePath, err := e.saveMedia(decoded, sequenceID, bodyType, index, imageType)
		if err != nil {
			// If save fails, leave the image inline
			continue
		}

		// Create media reference
		ref := models.MediaReference{
			Type:        fmt.Sprintf("image/%s", imageType),
			FilePath:    filePath,
			SHA256:      hashStr,
			SizeBytes:   int64(len(decoded)),
			Placeholder: placeholder,
		}
		references = append(references, ref)

		// Replace in body
		modifiedBody = strings.Replace(modifiedBody, fullMatch, placeholder, 1)
		index++
	}

	return modifiedBody, references, nil
}

// saveMedia saves decoded media content to disk
// Returns the relative file path
func (e *Extractor) saveMedia(data []byte, sequenceID uint64, bodyType string, index int, imageType string) (string, error) {
	// Create directory structure: {storage_path}/{YYYY-MM-DD}/
	now := time.Now()
	dateDir := now.Format("2006-01-02")
	fullDir := filepath.Join(e.storagePath, dateDir)

	// Create directory if it doesn't exist
	if err := os.MkdirAll(fullDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create media directory: %w", err)
	}

	// Generate filename: seq_{N}_{type}_{index}.{ext}
	filename := fmt.Sprintf("seq_%d_%s_%d.%s", sequenceID, bodyType, index, imageType)
	fullPath := filepath.Join(fullDir, filename)

	// Write file
	if err := os.WriteFile(fullPath, data, 0644); err != nil {
		return "", fmt.Errorf("failed to write media file: %w", err)
	}

	// Return relative path
	relativePath := filepath.Join(dateDir, filename)
	return relativePath, nil
}

// DetectBase64Images checks if body contains Base64 images
// Returns true if at least one Base64 image is detected
func DetectBase64Images(body string) bool {
	return base64ImagePattern.MatchString(body)
}

// EstimateBase64ImageSize estimates the total size of Base64 images in body
// Returns size in kilobytes
func EstimateBase64ImageSize(body string) int64 {
	matches := base64ImagePattern.FindAllStringSubmatch(body, -1)
	totalSizeKB := int64(0)

	for _, match := range matches {
		if len(match) < 3 {
			continue
		}

		base64Data := match[2]
		// Approximate decoded size
		decodedSize := (len(base64Data) * 3) / 4
		totalSizeKB += int64(decodedSize) / 1024
	}

	return totalSizeKB
}
