package proxy

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jnd-labs/aiblackbox/internal/audit"
	"github.com/jnd-labs/aiblackbox/internal/config"
	"github.com/jnd-labs/aiblackbox/internal/models"
)

// TestMediaExtraction_EndToEnd verifies media extraction works end-to-end
func TestMediaExtraction_EndToEnd(t *testing.T) {
	// Create temp directory for storage
	tempDir := t.TempDir()
	mediaDir := filepath.Join(tempDir, "media")
	auditFile := filepath.Join(tempDir, "audit.jsonl")

	// Create config with media extraction enabled
	cfg := &config.Config{
		Server: config.ServerConfig{
			Port:        8080,
			GenesisSeed: "test-seed",
		},
		Endpoints: []config.EndpointConfig{
			{Name: "test", Target: "http://example.com"},
		},
		Storage: config.StorageConfig{
			Path: auditFile,
		},
		Streaming: config.StreamingConfig{
			MaxAuditBodySize:       10485760,
			StreamTimeout:          300,
			EnableSequenceTracking: true,
		},
		Media: config.MediaConfig{
			EnableExtraction: true,
			MinSizeKB:        10, // Lower threshold for testing
			StoragePath:      mediaDir,
		},
	}

	// Create storage
	storage, err := audit.NewFileStorage(auditFile)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}

	// Create audit worker
	worker := audit.NewWorker(storage, cfg.Server.GenesisSeed, 100)

	// Create handler
	handler := NewHandler(cfg, worker)

	// Create mock upstream server
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return response with large Base64 image
		largeImage := strings.Repeat("ABCD", 5000) // ~20KB Base64
		response := `{
			"id": "chatcmpl-test",
			"choices": [{
				"message": {
					"role": "assistant",
					"content": "Here's an image: data:image/png;base64,` + largeImage + `"
				}
			}]
		}`
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(response))
	}))
	defer upstream.Close()

	// Update endpoint target to mock server
	cfg.Endpoints[0].Target = upstream.URL

	// Create request with large Base64 image
	largeRequestImage := strings.Repeat("EFGH", 5000) // ~20KB Base64
	requestBody := `{
		"model": "gpt-4",
		"messages": [{
			"role": "user",
			"content": "data:image/jpeg;base64,` + largeRequestImage + `"
		}]
	}`

	// Make request through proxy
	req := httptest.NewRequest("POST", "/test/chat/completions", strings.NewReader(requestBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	// Check response
	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", w.Code)
	}

	// Wait for audit worker to process
	time.Sleep(100 * time.Millisecond)
	worker.Shutdown()

	// Read audit log
	auditData, err := os.ReadFile(auditFile)
	if err != nil {
		t.Fatalf("Failed to read audit file: %v", err)
	}

	var entry models.AuditEntry
	if err := json.Unmarshal(auditData, &entry); err != nil {
		t.Fatalf("Failed to parse audit entry: %v", err)
	}

	// Verify request media was extracted
	if len(entry.Request.MediaReferences) != 1 {
		t.Fatalf("Expected 1 request media reference, got %d", len(entry.Request.MediaReferences))
	}

	reqMedia := entry.Request.MediaReferences[0]
	t.Logf("Request media extracted: type=%s, size=%d, path=%s, hash=%s",
		reqMedia.Type, reqMedia.SizeBytes, reqMedia.FilePath, reqMedia.SHA256[:16])

	// Verify request media details
	if reqMedia.Type != "image/jpeg" {
		t.Errorf("Expected request media type 'image/jpeg', got '%s'", reqMedia.Type)
	}

	if reqMedia.Placeholder != "[IMAGE_EXTRACTED:0]" {
		t.Errorf("Expected placeholder '[IMAGE_EXTRACTED:0]', got '%s'", reqMedia.Placeholder)
	}

	if reqMedia.SHA256 == "" || len(reqMedia.SHA256) != 64 {
		t.Errorf("Invalid SHA256 hash: '%s' (len=%d)", reqMedia.SHA256, len(reqMedia.SHA256))
	}

	if reqMedia.SizeBytes <= 0 {
		t.Error("Size should be positive")
	}

	// Verify request body contains placeholder, not original image
	if !strings.Contains(entry.Request.Body, "[IMAGE_EXTRACTED:0]") {
		t.Error("Request body should contain placeholder")
	}

	if strings.Contains(entry.Request.Body, largeRequestImage) {
		t.Error("Request body should not contain original image data")
	}

	// Verify response media was extracted
	if len(entry.Response.MediaReferences) != 1 {
		t.Fatalf("Expected 1 response media reference, got %d", len(entry.Response.MediaReferences))
	}

	respMedia := entry.Response.MediaReferences[0]
	t.Logf("Response media extracted: type=%s, size=%d, path=%s, hash=%s",
		respMedia.Type, respMedia.SizeBytes, respMedia.FilePath, respMedia.SHA256[:16])

	// Verify response media details
	if respMedia.Type != "image/png" {
		t.Errorf("Expected response media type 'image/png', got '%s'", respMedia.Type)
	}

	if respMedia.Placeholder != "[IMAGE_EXTRACTED:0]" {
		t.Errorf("Expected placeholder '[IMAGE_EXTRACTED:0]', got '%s'", respMedia.Placeholder)
	}

	// Verify response body contains placeholder, not original image
	if !strings.Contains(entry.Response.Body, "[IMAGE_EXTRACTED:0]") {
		t.Error("Response body should contain placeholder")
	}

	// Verify media files were created
	reqMediaPath := filepath.Join(mediaDir, reqMedia.FilePath)
	if _, err := os.Stat(reqMediaPath); os.IsNotExist(err) {
		t.Errorf("Expected request media file to exist at %s", reqMediaPath)
	} else {
		t.Logf("✓ Request media file exists: %s", reqMediaPath)
	}

	respMediaPath := filepath.Join(mediaDir, respMedia.FilePath)
	if _, err := os.Stat(respMediaPath); os.IsNotExist(err) {
		t.Errorf("Expected response media file to exist at %s", respMediaPath)
	} else {
		t.Logf("✓ Response media file exists: %s", respMediaPath)
	}

	// Verify file naming pattern
	// Expected: logs/media/{YYYY-MM-DD}/seq_{N}_{type}_{index}.{ext}
	if !strings.Contains(reqMedia.FilePath, "seq_0_request_0") {
		t.Errorf("Request media file path should contain 'seq_0_request_0', got: %s", reqMedia.FilePath)
	}

	if !strings.Contains(respMedia.FilePath, "seq_0_response_0") {
		t.Errorf("Response media file path should contain 'seq_0_response_0', got: %s", respMedia.FilePath)
	}

	t.Log("✓ End-to-end media extraction verified successfully")
}

// TestMediaExtraction_BelowThreshold verifies small images remain inline
func TestMediaExtraction_BelowThreshold(t *testing.T) {
	tempDir := t.TempDir()
	mediaDir := filepath.Join(tempDir, "media")
	auditFile := filepath.Join(tempDir, "audit.jsonl")

	cfg := &config.Config{
		Server: config.ServerConfig{
			Port:        8080,
			GenesisSeed: "test-seed",
		},
		Endpoints: []config.EndpointConfig{
			{Name: "test", Target: "http://example.com"},
		},
		Storage: config.StorageConfig{
			Path: auditFile,
		},
		Streaming: config.StreamingConfig{
			MaxAuditBodySize:       10485760,
			StreamTimeout:          300,
			EnableSequenceTracking: true,
		},
		Media: config.MediaConfig{
			EnableExtraction: true,
			MinSizeKB:        100, // High threshold
			StoragePath:      mediaDir,
		},
	}

	storage, err := audit.NewFileStorage(auditFile)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}

	worker := audit.NewWorker(storage, cfg.Server.GenesisSeed, 100)

	handler := NewHandler(cfg, worker)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Small image (< 100KB)
		smallImage := strings.Repeat("AB", 100) // ~200 bytes
		response := `{"content": "data:image/png;base64,` + smallImage + `"}`
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(response))
	}))
	defer upstream.Close()

	cfg.Endpoints[0].Target = upstream.URL

	smallImage := strings.Repeat("CD", 100)
	requestBody := `{"message": "data:image/png;base64,` + smallImage + `"}`

	req := httptest.NewRequest("POST", "/test/endpoint", strings.NewReader(requestBody))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	time.Sleep(100 * time.Millisecond)
	worker.Shutdown()

	auditData, err := os.ReadFile(auditFile)
	if err != nil {
		t.Fatalf("Failed to read audit file: %v", err)
	}

	var entry models.AuditEntry
	if err := json.Unmarshal(auditData, &entry); err != nil {
		t.Fatalf("Failed to parse audit entry: %v", err)
	}

	// Small images should remain inline
	if len(entry.Request.MediaReferences) != 0 {
		t.Errorf("Expected no request media references for small image, got %d", len(entry.Request.MediaReferences))
	}

	if len(entry.Response.MediaReferences) != 0 {
		t.Errorf("Expected no response media references for small image, got %d", len(entry.Response.MediaReferences))
	}

	// Original image data should still be in body
	if !strings.Contains(entry.Request.Body, smallImage) {
		t.Error("Small image should remain inline in request body")
	}

	t.Log("✓ Small images remain inline as expected")
}

// TestMediaExtraction_Disabled verifies extraction can be disabled
func TestMediaExtraction_Disabled(t *testing.T) {
	tempDir := t.TempDir()
	auditFile := filepath.Join(tempDir, "audit.jsonl")

	cfg := &config.Config{
		Server: config.ServerConfig{
			Port:        8080,
			GenesisSeed: "test-seed",
		},
		Endpoints: []config.EndpointConfig{
			{Name: "test", Target: "http://example.com"},
		},
		Storage: config.StorageConfig{
			Path: auditFile,
		},
		Streaming: config.StreamingConfig{
			MaxAuditBodySize:       10485760,
			StreamTimeout:          300,
			EnableSequenceTracking: true,
		},
		Media: config.MediaConfig{
			EnableExtraction: false, // DISABLED
			MinSizeKB:        10,
			StoragePath:      tempDir,
		},
	}

	storage, err := audit.NewFileStorage(auditFile)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}

	worker := audit.NewWorker(storage, cfg.Server.GenesisSeed, 100)

	handler := NewHandler(cfg, worker)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Read and echo request
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(body)
	}))
	defer upstream.Close()

	cfg.Endpoints[0].Target = upstream.URL

	largeImage := strings.Repeat("XY", 10000) // Large image
	requestBody := `{"image": "data:image/png;base64,` + largeImage + `"}`

	req := httptest.NewRequest("POST", "/test/endpoint", strings.NewReader(requestBody))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	time.Sleep(100 * time.Millisecond)
	worker.Shutdown()

	auditData, err := os.ReadFile(auditFile)
	if err != nil {
		t.Fatalf("Failed to read audit file: %v", err)
	}

	var entry models.AuditEntry
	if err := json.Unmarshal(auditData, &entry); err != nil {
		t.Fatalf("Failed to parse audit entry: %v", err)
	}

	// No extraction should occur when disabled
	if len(entry.Request.MediaReferences) != 0 {
		t.Errorf("Expected no media references when extraction disabled, got %d", len(entry.Request.MediaReferences))
	}

	// Original image should remain in body
	if !strings.Contains(entry.Request.Body, largeImage) {
		t.Error("Original image should remain in body when extraction is disabled")
	}

	t.Log("✓ Media extraction properly disabled")
}
