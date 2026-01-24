package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestNewResponseCapturer verifies basic capturer creation
func TestNewResponseCapturer(t *testing.T) {
	w := httptest.NewRecorder()
	capturer := NewResponseCapturer(w)

	if capturer.StatusCode() != http.StatusOK {
		t.Errorf("Expected default status 200, got %d", capturer.StatusCode())
	}

	if !capturer.IsComplete() {
		t.Error("Regular responses should be marked as complete by default")
	}

	if capturer.IsTruncated() {
		t.Error("New capturer should not be truncated")
	}
}

// TestNewStreamingResponseCapturer verifies streaming capturer creation
func TestNewStreamingResponseCapturer(t *testing.T) {
	w := httptest.NewRecorder()
	ctx := context.Background()
	maxSize := int64(1024)

	capturer := NewStreamingResponseCapturer(w, ctx, maxSize)

	if capturer.ctx != ctx {
		t.Error("Context not set correctly")
	}

	if capturer.maxSize != maxSize {
		t.Errorf("Expected maxSize %d, got %d", maxSize, capturer.maxSize)
	}

	if !capturer.IsComplete() {
		t.Error("Streaming responses should be marked as complete by default")
	}
}

// TestWriteAndCapture verifies basic write and capture functionality
func TestWriteAndCapture(t *testing.T) {
	w := httptest.NewRecorder()
	capturer := NewResponseCapturer(w)

	data := []byte("Hello, World!")
	n, err := capturer.Write(data)

	if err != nil {
		t.Fatalf("Unexpected write error: %v", err)
	}

	if n != len(data) {
		t.Errorf("Expected to write %d bytes, wrote %d", len(data), n)
	}

	if capturer.Body() != string(data) {
		t.Errorf("Expected body '%s', got '%s'", string(data), capturer.Body())
	}

	// Verify data was forwarded to underlying writer
	if w.Body.String() != string(data) {
		t.Errorf("Data not forwarded to underlying writer")
	}
}

// TestMultipleWrites verifies that multiple writes are accumulated
func TestMultipleWrites(t *testing.T) {
	w := httptest.NewRecorder()
	capturer := NewResponseCapturer(w)

	writes := []string{"Hello", ", ", "World", "!"}
	expected := strings.Join(writes, "")

	for _, data := range writes {
		_, err := capturer.Write([]byte(data))
		if err != nil {
			t.Fatalf("Unexpected write error: %v", err)
		}
	}

	if capturer.Body() != expected {
		t.Errorf("Expected body '%s', got '%s'", expected, capturer.Body())
	}
}

// TestWriteHeader verifies status code and header capture
func TestWriteHeader(t *testing.T) {
	w := httptest.NewRecorder()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Custom-Header", "test-value")

	capturer := NewResponseCapturer(w)
	capturer.WriteHeader(http.StatusCreated)

	if capturer.StatusCode() != http.StatusCreated {
		t.Errorf("Expected status %d, got %d", http.StatusCreated, capturer.StatusCode())
	}

	headers := capturer.Headers()
	if headers.Get("Content-Type") != "application/json" {
		t.Error("Content-Type header not captured")
	}

	if headers.Get("X-Custom-Header") != "test-value" {
		t.Error("Custom header not captured")
	}
}

// TestBufferTruncation verifies that large responses are truncated
func TestBufferTruncation(t *testing.T) {
	w := httptest.NewRecorder()
	ctx := context.Background()
	maxSize := int64(100) // Small limit for testing

	capturer := NewStreamingResponseCapturer(w, ctx, maxSize)

	// Write data that exceeds the limit
	largeData := strings.Repeat("A", 150)
	_, err := capturer.Write([]byte(largeData))

	if err != nil {
		t.Fatalf("Unexpected write error: %v", err)
	}

	if !capturer.IsTruncated() {
		t.Error("Expected response to be marked as truncated")
	}

	body := capturer.Body()
	if !strings.Contains(body, "[TRUNCATED:") {
		t.Error("Expected truncation marker in body")
	}

	// Verify only maxSize bytes were captured (plus truncation marker)
	bodyWithoutMarker := strings.Split(body, "\n[TRUNCATED:")[0]
	if int64(len(bodyWithoutMarker)) > maxSize {
		t.Errorf("Expected captured body <= %d bytes, got %d", maxSize, len(bodyWithoutMarker))
	}

	// Verify TruncatedAtBytes returns the full size
	if capturer.TruncatedAtBytes() != int64(len(largeData)) {
		t.Errorf("Expected TruncatedAtBytes to be %d, got %d", len(largeData), capturer.TruncatedAtBytes())
	}

	// Verify data was still forwarded to client
	if w.Body.String() != largeData {
		t.Error("Full data should be forwarded to client despite truncation")
	}
}

// TestNoTruncationWithUnlimitedBuffer verifies unlimited buffer (-1) works
func TestNoTruncationWithUnlimitedBuffer(t *testing.T) {
	w := httptest.NewRecorder()
	ctx := context.Background()
	maxSize := int64(-1) // Unlimited

	capturer := NewStreamingResponseCapturer(w, ctx, maxSize)

	// Write large data
	largeData := strings.Repeat("B", 10000)
	_, err := capturer.Write([]byte(largeData))

	if err != nil {
		t.Fatalf("Unexpected write error: %v", err)
	}

	if capturer.IsTruncated() {
		t.Error("Should not be truncated with unlimited buffer")
	}

	if capturer.Body() != largeData {
		t.Error("Full body should be captured with unlimited buffer")
	}
}

// TestCompletionCallback verifies callback is called exactly once
func TestCompletionCallback(t *testing.T) {
	w := httptest.NewRecorder()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	capturer := NewStreamingResponseCapturer(w, ctx, 1024)

	callCount := 0
	capturer.SetCompletionCallback(func() {
		callCount++
	})

	// Start monitoring
	go capturer.StartMonitoring()

	// Write some data
	capturer.Write([]byte("test data"))

	// Cancel context to trigger completion
	cancel()

	// Wait for callback
	time.Sleep(50 * time.Millisecond)

	if callCount != 1 {
		t.Errorf("Expected callback to be called exactly once, called %d times", callCount)
	}

	// Try to finalize again - should not call callback again
	capturer.finalize()
	capturer.finalize()

	if callCount != 1 {
		t.Errorf("Expected callback to still be called only once, called %d times", callCount)
	}
}

// TestContextCancellationDetection verifies client disconnect detection
func TestContextCancellationDetection(t *testing.T) {
	w := httptest.NewRecorder()
	ctx, cancel := context.WithCancel(context.Background())

	capturer := NewStreamingResponseCapturer(w, ctx, 1024)

	callbackCalled := false
	capturer.SetCompletionCallback(func() {
		callbackCalled = true
	})

	// Start monitoring
	go capturer.StartMonitoring()

	// Simulate client disconnect
	cancel()

	// Wait for callback
	time.Sleep(50 * time.Millisecond)

	if !callbackCalled {
		t.Error("Callback should be called on context cancellation")
	}

	if capturer.Error() != "CLIENT_DISCONNECT" {
		t.Errorf("Expected error 'CLIENT_DISCONNECT', got '%s'", capturer.Error())
	}

	if capturer.IsComplete() {
		t.Error("Response should not be marked as complete after disconnect")
	}
}

// TestTimeoutDetection verifies timeout detection
func TestTimeoutDetection(t *testing.T) {
	w := httptest.NewRecorder()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	capturer := NewStreamingResponseCapturer(w, ctx, 1024)

	callbackCalled := false
	capturer.SetCompletionCallback(func() {
		callbackCalled = true
	})

	// Start monitoring
	go capturer.StartMonitoring()

	// Wait for timeout
	time.Sleep(100 * time.Millisecond)

	if !callbackCalled {
		t.Error("Callback should be called on timeout")
	}

	if capturer.Error() != "STREAM_TIMEOUT" {
		t.Errorf("Expected error 'STREAM_TIMEOUT', got '%s'", capturer.Error())
	}

	if capturer.IsComplete() {
		t.Error("Response should not be marked as complete after timeout")
	}
}

// TestWriteErrorDetection verifies write error handling
func TestWriteErrorDetection(t *testing.T) {
	// Use a custom ResponseWriter that returns an error on write
	w := &errorWriter{err: http.ErrAbortHandler}
	ctx := context.Background()

	capturer := NewStreamingResponseCapturer(w, ctx, 1024)

	callbackCalled := false
	capturer.SetCompletionCallback(func() {
		callbackCalled = true
	})

	// Try to write - should trigger error
	_, err := capturer.Write([]byte("test"))

	if err == nil {
		t.Fatal("Expected write error")
	}

	if !callbackCalled {
		t.Error("Callback should be called on write error")
	}

	if !strings.Contains(capturer.Error(), "WRITE_ERROR") {
		t.Errorf("Expected error to contain 'WRITE_ERROR', got '%s'", capturer.Error())
	}

	if capturer.IsComplete() {
		t.Error("Response should not be marked as complete after write error")
	}
}

// TestFlush verifies Flush implementation
func TestFlush(t *testing.T) {
	w := httptest.NewRecorder()
	capturer := NewResponseCapturer(w)

	// Write some data
	capturer.Write([]byte("test"))

	// Flush should not panic (httptest.ResponseRecorder implements Flusher)
	capturer.Flush()

	// No error expected
	if capturer.Body() != "test" {
		t.Error("Flush should not affect captured data")
	}
}

// errorWriter is a mock ResponseWriter that returns errors on Write
type errorWriter struct {
	header http.Header
	err    error
}

func (w *errorWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *errorWriter) Write([]byte) (int, error) {
	return 0, w.err
}

func (w *errorWriter) WriteHeader(statusCode int) {
	// No-op
}
