package proxy

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jnd-labs/aiblackbox/internal/audit"
	"github.com/jnd-labs/aiblackbox/internal/config"
	"github.com/jnd-labs/aiblackbox/internal/models"
)

// TestHandlerRegularRequest verifies regular (non-streaming) request handling
func TestHandlerRegularRequest(t *testing.T) {
	// Create mock backend
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"message": "success"}`))
	}))
	defer backend.Close()

	// Create test config
	cfg := createTestConfig(backend.URL)
	storage := &mockAuditStorage{}
	worker := audit.NewWorker(storage, "test-seed", 10)
	defer worker.Shutdown()

	handler := NewHandler(cfg, worker)

	// Create test request
	req := httptest.NewRequest("POST", "/test/api/endpoint", strings.NewReader(`{"test": "data"}`))
	w := httptest.NewRecorder()

	// Handle request
	handler.ServeHTTP(w, req)

	// Wait for audit processing
	time.Sleep(50 * time.Millisecond)

	// Verify response
	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	// Verify audit entry
	if len(storage.entries) != 1 {
		t.Fatalf("Expected 1 audit entry, got %d", len(storage.entries))
	}

	entry := storage.entries[0]
	if entry.Endpoint != "test" {
		t.Errorf("Expected endpoint 'test', got '%s'", entry.Endpoint)
	}

	if entry.Request.Body != `{"test": "data"}` {
		t.Errorf("Request body not captured correctly")
	}

	if entry.Response.Body != `{"message": "success"}` {
		t.Errorf("Response body not captured correctly")
	}

	if !entry.Response.IsComplete {
		t.Error("Response should be marked as complete")
	}

	if entry.Response.IsStreaming {
		t.Error("Response should not be marked as streaming")
	}
}

// TestHandlerStreamingRequest verifies streaming (SSE) request handling
func TestHandlerStreamingRequest(t *testing.T) {
	// Create mock SSE backend
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("ResponseWriter doesn't support flushing")
		}

		// Send SSE events
		for i := 0; i < 3; i++ {
			fmt.Fprintf(w, "data: event %d\n\n", i)
			flusher.Flush()
			time.Sleep(10 * time.Millisecond)
		}
	}))
	defer backend.Close()

	// Create test config with streaming enabled
	cfg := createTestConfig(backend.URL)
	storage := &mockAuditStorage{}
	worker := audit.NewWorker(storage, "test-seed", 10)
	defer worker.Shutdown()

	handler := NewHandler(cfg, worker)

	// Create streaming request
	req := httptest.NewRequest("POST", "/test/stream", strings.NewReader(`{"stream": true}`))
	req.Header.Set("Accept", "text/event-stream")
	w := httptest.NewRecorder()

	// Handle request
	handler.ServeHTTP(w, req)

	// Wait for audit processing (streaming finalization happens asynchronously)
	time.Sleep(100 * time.Millisecond)

	// Verify response contains events
	body := w.Body.String()
	if !strings.Contains(body, "event 0") {
		t.Error("Response should contain streamed events")
	}

	// Verify audit entry
	if len(storage.entries) != 1 {
		t.Fatalf("Expected 1 audit entry, got %d", len(storage.entries))
	}

	entry := storage.entries[0]
	if !entry.Response.IsStreaming {
		t.Error("Response should be marked as streaming")
	}

	if !entry.Response.IsComplete {
		t.Error("Streaming response should be marked as complete")
	}

	if !strings.Contains(entry.Response.Body, "event") {
		t.Error("Audit should capture streamed content")
	}
}

// TestHandlerSequenceIDAssignment verifies sequence IDs are assigned correctly
func TestHandlerSequenceIDAssignment(t *testing.T) {
	// Create mock backend
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer backend.Close()

	// Create test config
	cfg := createTestConfig(backend.URL)
	storage := &mockAuditStorage{}
	worker := audit.NewWorker(storage, "test-seed", 100)
	defer worker.Shutdown()

	handler := NewHandler(cfg, worker)

	// Send multiple requests
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest("POST", "/test/endpoint", strings.NewReader(fmt.Sprintf(`{"id": %d}`, i)))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
	}

	// Wait for audit processing
	time.Sleep(100 * time.Millisecond)

	// Verify sequence IDs
	if len(storage.entries) != 5 {
		t.Fatalf("Expected 5 audit entries, got %d", len(storage.entries))
	}

	for i, entry := range storage.entries {
		if entry.SequenceID != uint64(i) {
			t.Errorf("Entry %d has wrong sequence ID: expected %d, got %d", i, i, entry.SequenceID)
		}
	}

	// Verify hash chain
	for i := 1; i < len(storage.entries); i++ {
		if storage.entries[i].PrevHash != storage.entries[i-1].Hash {
			t.Errorf("Entry %d: Hash chain broken", i)
		}
	}
}

// TestHandlerConcurrentRequests verifies thread-safety with concurrent requests
func TestHandlerConcurrentRequests(t *testing.T) {
	// Create mock backend
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate variable response times
		time.Sleep(time.Millisecond * time.Duration(10+r.URL.Query().Get("delay")[0]%10))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer backend.Close()

	// Create test config
	cfg := createTestConfig(backend.URL)
	storage := &mockAuditStorage{}
	worker := audit.NewWorker(storage, "test-seed", 100)
	defer worker.Shutdown()

	handler := NewHandler(cfg, worker)

	// Send concurrent requests
	done := make(chan bool)
	numRequests := 20

	for i := 0; i < numRequests; i++ {
		go func(id int) {
			req := httptest.NewRequest("POST", fmt.Sprintf("/test/endpoint?delay=%d", id),
				strings.NewReader(fmt.Sprintf(`{"id": %d}`, id)))
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			done <- true
		}(i)
	}

	// Wait for all requests
	for i := 0; i < numRequests; i++ {
		<-done
	}

	// Wait for audit processing
	time.Sleep(200 * time.Millisecond)

	// Verify all entries processed
	if len(storage.entries) != numRequests {
		t.Fatalf("Expected %d audit entries, got %d", numRequests, len(storage.entries))
	}

	// Verify sequence IDs are in order
	for i, entry := range storage.entries {
		if entry.SequenceID != uint64(i) {
			t.Errorf("Entry at index %d has wrong sequence ID: expected %d, got %d", i, i, entry.SequenceID)
		}
	}

	// Verify hash chain integrity
	for i := 1; i < len(storage.entries); i++ {
		if storage.entries[i].PrevHash != storage.entries[i-1].Hash {
			t.Errorf("Entry %d: Hash chain broken", i)
		}
	}
}

// TestHandlerStreamingWithTimeout verifies timeout handling for streaming
func TestHandlerStreamingWithTimeout(t *testing.T) {
	// Create mock backend that streams indefinitely
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		flusher, ok := w.(http.Flusher)
		if !ok {
			return
		}

		// Stream for a long time
		for i := 0; i < 100; i++ {
			fmt.Fprintf(w, "data: event %d\n\n", i)
			flusher.Flush()
			time.Sleep(100 * time.Millisecond)
		}
	}))
	defer backend.Close()

	// Create test config with short timeout
	cfg := createTestConfig(backend.URL)
	cfg.Streaming.StreamTimeout = 1 // 1 second timeout
	storage := &mockAuditStorage{}
	worker := audit.NewWorker(storage, "test-seed", 10)
	defer worker.Shutdown()

	handler := NewHandler(cfg, worker)

	// Create streaming request with context that will timeout
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	req := httptest.NewRequest("POST", "/test/stream", strings.NewReader(`{}`))
	req = req.WithContext(ctx)
	req.Header.Set("Accept", "text/event-stream")
	w := httptest.NewRecorder()

	// Handle request (will timeout)
	handler.ServeHTTP(w, req)

	// Wait for audit processing
	time.Sleep(200 * time.Millisecond)

	// Verify audit entry shows timeout
	if len(storage.entries) != 1 {
		t.Fatalf("Expected 1 audit entry, got %d", len(storage.entries))
	}

	entry := storage.entries[0]
	if entry.Response.IsComplete {
		t.Error("Response should not be marked as complete after timeout")
	}

	// Error should indicate timeout or client disconnect
	if entry.Response.Error == "" {
		t.Error("Expected error to be set for incomplete stream")
	}
}

// TestHandlerBufferTruncation verifies buffer truncation for large responses
func TestHandlerBufferTruncation(t *testing.T) {
	// Create mock backend with large response
	largeBody := strings.Repeat("A", 20000) // 20KB
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(largeBody))
	}))
	defer backend.Close()

	// Create test config with small buffer limit
	cfg := createTestConfig(backend.URL)
	cfg.Streaming.MaxAuditBodySize = 5000 // 5KB limit
	storage := &mockAuditStorage{}
	worker := audit.NewWorker(storage, "test-seed", 10)
	defer worker.Shutdown()

	handler := NewHandler(cfg, worker)

	// Create streaming request
	req := httptest.NewRequest("POST", "/test/stream", strings.NewReader(`{}`))
	req.Header.Set("Accept", "text/event-stream")
	w := httptest.NewRecorder()

	// Handle request
	handler.ServeHTTP(w, req)

	// Wait for audit processing
	time.Sleep(100 * time.Millisecond)

	// Verify full response was sent to client
	if w.Body.Len() != len(largeBody) {
		t.Errorf("Client should receive full response: expected %d bytes, got %d", len(largeBody), w.Body.Len())
	}

	// Verify audit entry shows truncation
	if len(storage.entries) != 1 {
		t.Fatalf("Expected 1 audit entry, got %d", len(storage.entries))
	}

	entry := storage.entries[0]
	if !entry.Response.Truncated {
		t.Error("Response should be marked as truncated")
	}

	if entry.Response.TruncatedAtBytes != int64(len(largeBody)) {
		t.Errorf("Expected TruncatedAtBytes to be %d, got %d", len(largeBody), entry.Response.TruncatedAtBytes)
	}

	// Audit body should be truncated
	if int64(len(entry.Response.Body)) > cfg.Streaming.MaxAuditBodySize+200 {
		t.Errorf("Audit body should be truncated to around %d bytes, got %d", cfg.Streaming.MaxAuditBodySize, len(entry.Response.Body))
	}

	if !strings.Contains(entry.Response.Body, "[TRUNCATED:") {
		t.Error("Audit body should contain truncation marker")
	}
}

// Helper: mockAuditStorage for testing
type mockAuditStorage struct {
	entries []*models.AuditEntry
}

func (m *mockAuditStorage) Write(entry *models.AuditEntry) error {
	m.entries = append(m.entries, entry)
	return nil
}

func (m *mockAuditStorage) Close() error {
	return nil
}

// Helper: createTestConfig creates a test configuration
func createTestConfig(backendURL string) *config.Config {
	return &config.Config{
		Server: config.ServerConfig{
			Port:        8080,
			GenesisSeed: "test-seed",
		},
		Endpoints: []config.EndpointConfig{
			{Name: "test", Target: backendURL},
		},
		Storage: config.StorageConfig{
			Path: "/tmp/test-audit.jsonl",
		},
		Streaming: config.StreamingConfig{
			MaxAuditBodySize:       10485760, // 10 MB
			StreamTimeout:          300,      // 5 minutes
			EnableSequenceTracking: true,
		},
	}
}
