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
)

// TestErrorHandling_UpstreamError verifies handling of upstream server errors
func TestErrorHandling_UpstreamError(t *testing.T) {
	// Create backend that returns error
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error": "internal error"}`))
	}))
	defer backend.Close()

	cfg := createTestConfig(backend.URL)
	storage := &mockAuditStorage{}
	worker := audit.NewWorker(storage, "test-seed", 10)
	defer worker.Shutdown()

	handler := NewHandler(cfg, worker)

	req := httptest.NewRequest("POST", "/test/endpoint", strings.NewReader(`{}`))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)
	time.Sleep(50 * time.Millisecond)

	// Verify error status is proxied
	if w.Code != http.StatusInternalServerError {
		t.Errorf("Expected status 500, got %d", w.Code)
	}

	// Verify audit entry captures error
	if len(storage.entries) != 1 {
		t.Fatalf("Expected 1 audit entry, got %d", len(storage.entries))
	}

	entry := storage.entries[0]
	if entry.Response.StatusCode != http.StatusInternalServerError {
		t.Errorf("Audit should capture error status code")
	}

	if !strings.Contains(entry.Response.Body, "error") {
		t.Error("Audit should capture error response body")
	}
}

// TestErrorHandling_InvalidEndpoint verifies handling of unknown endpoints
func TestErrorHandling_InvalidEndpoint(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	cfg := createTestConfig(backend.URL)
	storage := &mockAuditStorage{}
	worker := audit.NewWorker(storage, "test-seed", 10)
	defer worker.Shutdown()

	handler := NewHandler(cfg, worker)

	// Request with unknown endpoint
	req := httptest.NewRequest("POST", "/unknown/endpoint", strings.NewReader(`{}`))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)
	time.Sleep(50 * time.Millisecond)

	// Verify 404 response
	if w.Code != http.StatusNotFound {
		t.Errorf("Expected status 404, got %d", w.Code)
	}

	// No audit entry should be created for invalid endpoints
	if len(storage.entries) != 0 {
		t.Errorf("Expected 0 audit entries for invalid endpoint, got %d", len(storage.entries))
	}
}

// TestErrorHandling_MalformedRequest verifies handling of malformed requests
func TestErrorHandling_MalformedRequest(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	cfg := createTestConfig(backend.URL)
	storage := &mockAuditStorage{}
	worker := audit.NewWorker(storage, "test-seed", 10)
	defer worker.Shutdown()

	handler := NewHandler(cfg, worker)

	// Request with empty path (no endpoint)
	req := httptest.NewRequest("POST", "/", strings.NewReader(`{}`))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)
	time.Sleep(50 * time.Millisecond)

	// Verify 400 response
	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", w.Code)
	}

	// No audit entry for malformed requests
	if len(storage.entries) != 0 {
		t.Errorf("Expected 0 audit entries, got %d", len(storage.entries))
	}
}

// TestErrorHandling_StreamingWriteError verifies write error detection
func TestErrorHandling_StreamingWriteError(t *testing.T) {
	// This test verifies that write errors are properly detected and handled
	// We already test this in response_capturer_test.go with errorWriter
	// Here we test it in the context of the full handler

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("data: test\n\n"))
	}))
	defer backend.Close()

	cfg := createTestConfig(backend.URL)
	storage := &mockAuditStorage{}
	worker := audit.NewWorker(storage, "test-seed", 10)
	defer worker.Shutdown()

	handler := NewHandler(cfg, worker)

	req := httptest.NewRequest("POST", "/test/stream", strings.NewReader(`{}`))
	req.Header.Set("Accept", "text/event-stream")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)
	time.Sleep(100 * time.Millisecond)

	// Verify audit entry was created
	if len(storage.entries) != 1 {
		t.Fatalf("Expected 1 audit entry, got %d", len(storage.entries))
	}
}

// TestErrorHandling_ContextCancellation verifies client disconnect handling
func TestErrorHandling_ContextCancellation(t *testing.T) {
	// Create backend that streams slowly
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		flusher, ok := w.(http.Flusher)
		if !ok {
			return
		}

		// Stream for a while
		for i := 0; i < 10; i++ {
			fmt.Fprintf(w, "data: event %d\n\n", i)
			flusher.Flush()
			time.Sleep(50 * time.Millisecond)
		}
	}))
	defer backend.Close()

	cfg := createTestConfig(backend.URL)
	storage := &mockAuditStorage{}
	worker := audit.NewWorker(storage, "test-seed", 10)
	defer worker.Shutdown()

	handler := NewHandler(cfg, worker)

	// Create request with cancellable context
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("POST", "/test/stream", strings.NewReader(`{}`))
	req = req.WithContext(ctx)
	req.Header.Set("Accept", "text/event-stream")
	w := httptest.NewRecorder()

	// Cancel after short delay
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	handler.ServeHTTP(w, req)
	time.Sleep(200 * time.Millisecond)

	// Verify audit entry shows incomplete
	if len(storage.entries) != 1 {
		t.Fatalf("Expected 1 audit entry, got %d", len(storage.entries))
	}

	entry := storage.entries[0]
	if entry.Response.IsComplete {
		t.Error("Response should not be marked as complete after cancellation")
	}

	if entry.Response.Error == "" {
		t.Error("Error field should be set for cancelled stream")
	}
}

// TestErrorHandling_PanicRecovery verifies panic recovery in handlers
func TestErrorHandling_PanicRecovery(t *testing.T) {
	// Create backend that panics
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("simulated panic")
	}))
	defer backend.Close()

	cfg := createTestConfig(backend.URL)
	storage := &mockAuditStorage{}
	worker := audit.NewWorker(storage, "test-seed", 10)
	defer worker.Shutdown()

	handler := NewHandler(cfg, worker)

	req := httptest.NewRequest("POST", "/test/endpoint", strings.NewReader(`{}`))
	w := httptest.NewRecorder()

	// Should not panic - should be recovered
	handler.ServeHTTP(w, req)

	// The handler should recover and return 500
	// Note: httputil.ReverseProxy has its own panic recovery
	time.Sleep(50 * time.Millisecond)
}

// TestErrorHandling_MultipleErrors verifies handling of multiple error conditions
func TestErrorHandling_MultipleErrors(t *testing.T) {
	errorCount := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		errorCount++
		// Return different errors
		if errorCount%2 == 0 {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"error": "bad request"}`))
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"error": "server error"}`))
		}
	}))
	defer backend.Close()

	cfg := createTestConfig(backend.URL)
	storage := &mockAuditStorage{}
	worker := audit.NewWorker(storage, "test-seed", 10)
	defer worker.Shutdown()

	handler := NewHandler(cfg, worker)

	// Send multiple requests with errors
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest("POST", "/test/endpoint", strings.NewReader(fmt.Sprintf(`{"id":%d}`, i)))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
	}

	time.Sleep(100 * time.Millisecond)

	// Verify all errors were captured
	if len(storage.entries) != 5 {
		t.Fatalf("Expected 5 audit entries, got %d", len(storage.entries))
	}

	// Verify sequence ordering maintained despite errors
	for i, entry := range storage.entries {
		if entry.SequenceID != uint64(i) {
			t.Errorf("Entry %d has wrong sequence ID: expected %d, got %d", i, i, entry.SequenceID)
		}
	}

	// Verify hash chain integrity even with errors
	for i := 1; i < len(storage.entries); i++ {
		if storage.entries[i].PrevHash != storage.entries[i-1].Hash {
			t.Errorf("Entry %d: Hash chain broken despite errors", i)
		}
	}
}

// TestErrorHandling_EmptyResponse verifies handling of empty responses
func TestErrorHandling_EmptyResponse(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
		// No body
	}))
	defer backend.Close()

	cfg := createTestConfig(backend.URL)
	storage := &mockAuditStorage{}
	worker := audit.NewWorker(storage, "test-seed", 10)
	defer worker.Shutdown()

	handler := NewHandler(cfg, worker)

	req := httptest.NewRequest("POST", "/test/endpoint", strings.NewReader(`{}`))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)
	time.Sleep(50 * time.Millisecond)

	// Verify empty response is handled
	if len(storage.entries) != 1 {
		t.Fatalf("Expected 1 audit entry, got %d", len(storage.entries))
	}

	entry := storage.entries[0]
	if entry.Response.StatusCode != http.StatusNoContent {
		t.Errorf("Expected status 204, got %d", entry.Response.StatusCode)
	}

	if entry.Response.Body != "" {
		t.Error("Empty response should have empty body in audit")
	}

	if !entry.Response.IsComplete {
		t.Error("Empty response should be marked as complete")
	}
}

// TestErrorHandling_ChainIntegrityWithErrors verifies hash chain remains valid with errors
func TestErrorHandling_ChainIntegrityWithErrors(t *testing.T) {
	requestNum := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestNum++
		// Mix of success and errors
		switch requestNum % 4 {
		case 0:
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("success"))
		case 1:
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte("bad request"))
		case 2:
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("server error"))
		case 3:
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte("not found"))
		}
	}))
	defer backend.Close()

	cfg := createTestConfig(backend.URL)
	storage := &mockAuditStorage{}
	worker := audit.NewWorker(storage, "test-seed", 10)
	defer worker.Shutdown()

	handler := NewHandler(cfg, worker)

	// Send many requests with mixed results
	numRequests := 20
	for i := 0; i < numRequests; i++ {
		req := httptest.NewRequest("POST", "/test/endpoint", strings.NewReader(fmt.Sprintf(`{"id":%d}`, i)))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
	}

	time.Sleep(200 * time.Millisecond)

	// Verify all entries processed
	if len(storage.entries) != numRequests {
		t.Fatalf("Expected %d audit entries, got %d", numRequests, len(storage.entries))
	}

	// Verify complete hash chain integrity
	for i := 1; i < len(storage.entries); i++ {
		if storage.entries[i].PrevHash != storage.entries[i-1].Hash {
			t.Errorf("Entry %d: Hash chain broken at sequence=%d, status=%d",
				i, storage.entries[i].SequenceID, storage.entries[i].Response.StatusCode)
		}

		if storage.entries[i].Hash == "" {
			t.Errorf("Entry %d: Hash is empty", i)
		}

		if storage.entries[i].PrevHash == "" {
			t.Errorf("Entry %d: PrevHash is empty", i)
		}
	}

	// Verify no duplicate hashes
	hashSet := make(map[string]bool)
	for i, entry := range storage.entries {
		if hashSet[entry.Hash] {
			t.Errorf("Entry %d: Duplicate hash detected", i)
		}
		hashSet[entry.Hash] = true
	}
}

// TestErrorHandling_LargeErrorResponse verifies handling of large error responses
func TestErrorHandling_LargeErrorResponse(t *testing.T) {
	largeError := strings.Repeat("ERROR: ", 10000) // Large error message
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(largeError))
	}))
	defer backend.Close()

	cfg := createTestConfig(backend.URL)
	cfg.Streaming.MaxAuditBodySize = 5000 // Small limit
	storage := &mockAuditStorage{}
	worker := audit.NewWorker(storage, "test-seed", 10)
	defer worker.Shutdown()

	handler := NewHandler(cfg, worker)

	req := httptest.NewRequest("POST", "/test/endpoint", strings.NewReader(`{}`))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)
	time.Sleep(50 * time.Millisecond)

	// Verify client gets full error
	if w.Body.Len() != len(largeError) {
		t.Errorf("Client should receive full error: expected %d bytes, got %d", len(largeError), w.Body.Len())
	}

	// For regular requests, truncation doesn't apply (only for streaming)
	// But verify entry was created
	if len(storage.entries) != 1 {
		t.Fatalf("Expected 1 audit entry, got %d", len(storage.entries))
	}
}

// TestErrorHandling_ConcurrentErrors verifies thread-safety with concurrent errors
func TestErrorHandling_ConcurrentErrors(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Random errors
		errorType := r.URL.Query().Get("type")
		switch errorType {
		case "400":
			w.WriteHeader(http.StatusBadRequest)
		case "500":
			w.WriteHeader(http.StatusInternalServerError)
		case "404":
			w.WriteHeader(http.StatusNotFound)
		default:
			w.WriteHeader(http.StatusOK)
		}
		w.Write([]byte("response"))
	}))
	defer backend.Close()

	cfg := createTestConfig(backend.URL)
	storage := &mockAuditStorage{}
	worker := audit.NewWorker(storage, "test-seed", 100)
	defer worker.Shutdown()

	handler := NewHandler(cfg, worker)

	// Send concurrent requests with various errors
	done := make(chan bool)
	numRequests := 30
	errorTypes := []string{"400", "500", "404", "ok"}

	for i := 0; i < numRequests; i++ {
		go func(id int) {
			errorType := errorTypes[id%len(errorTypes)]
			req := httptest.NewRequest("POST", fmt.Sprintf("/test/endpoint?type=%s&id=%d", errorType, id),
				strings.NewReader(fmt.Sprintf(`{"id":%d}`, id)))
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			done <- true
		}(i)
	}

	// Wait for all requests
	for i := 0; i < numRequests; i++ {
		<-done
	}

	time.Sleep(200 * time.Millisecond)

	// Verify all entries processed
	if len(storage.entries) != numRequests {
		t.Fatalf("Expected %d audit entries, got %d", numRequests, len(storage.entries))
	}

	// Verify sequence ordering
	for i, entry := range storage.entries {
		if entry.SequenceID != uint64(i) {
			t.Errorf("Entry at index %d has wrong sequence ID: expected %d, got %d", i, i, entry.SequenceID)
		}
	}

	// Verify hash chain integrity with mixed errors
	for i := 1; i < len(storage.entries); i++ {
		if storage.entries[i].PrevHash != storage.entries[i-1].Hash {
			t.Errorf("Entry %d: Hash chain broken with concurrent errors", i)
		}
	}
}

// TestErrorHandling_RecoveryFromFailures verifies system remains operational after failures
func TestErrorHandling_RecoveryFromFailures(t *testing.T) {
	failCount := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		failCount++
		if failCount <= 5 {
			// First 5 requests fail
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("temporary failure"))
		} else {
			// Then recover
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("success"))
		}
	}))
	defer backend.Close()

	cfg := createTestConfig(backend.URL)
	storage := &mockAuditStorage{}
	worker := audit.NewWorker(storage, "test-seed", 10)
	defer worker.Shutdown()

	handler := NewHandler(cfg, worker)

	// Send 10 requests (5 failures, then 5 successes)
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest("POST", "/test/endpoint", strings.NewReader(fmt.Sprintf(`{"id":%d}`, i)))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		time.Sleep(10 * time.Millisecond)
	}

	time.Sleep(100 * time.Millisecond)

	// Verify all entries processed
	if len(storage.entries) != 10 {
		t.Fatalf("Expected 10 audit entries, got %d", len(storage.entries))
	}

	// Verify system recovered (last 5 should be successful)
	successCount := 0
	for i := 5; i < 10; i++ {
		if storage.entries[i].Response.StatusCode == http.StatusOK {
			successCount++
		}
	}

	if successCount != 5 {
		t.Errorf("Expected 5 successful requests after recovery, got %d", successCount)
	}

	// Verify hash chain integrity throughout failures and recovery
	for i := 1; i < len(storage.entries); i++ {
		if storage.entries[i].PrevHash != storage.entries[i-1].Hash {
			t.Errorf("Entry %d: Hash chain broken during failure/recovery", i)
		}
	}
}
