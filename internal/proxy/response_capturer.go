package proxy

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
)

// ResponseCapturer wraps http.ResponseWriter to capture response data
// Supports both regular responses and streaming (SSE) responses
// Enhanced with completion callbacks and buffer management for streaming
type ResponseCapturer struct {
	http.ResponseWriter

	// Core capture fields
	statusCode int
	body       strings.Builder
	headers    http.Header

	// Streaming support
	ctx        context.Context
	onComplete func()
	completed  atomic.Bool

	// Buffer management
	currentSize int64
	maxSize     int64
	truncated   bool

	// Error tracking
	errorMsg   string
	isComplete bool
	writeErr   error
}

// NewResponseCapturer creates a new response capturer for regular (non-streaming) responses
func NewResponseCapturer(w http.ResponseWriter) *ResponseCapturer {
	return &ResponseCapturer{
		ResponseWriter: w,
		statusCode:     http.StatusOK,
		headers:        make(http.Header),
		isComplete:     true, // Regular responses are always complete
		maxSize:        -1,   // No limit for regular responses
	}
}

// NewStreamingResponseCapturer creates a response capturer for streaming responses
// ctx: context for monitoring client disconnection
// maxSize: maximum body size to capture (bytes), -1 for unlimited
func NewStreamingResponseCapturer(w http.ResponseWriter, ctx context.Context, maxSize int64) *ResponseCapturer {
	rc := &ResponseCapturer{
		ResponseWriter: w,
		statusCode:     http.StatusOK,
		headers:        make(http.Header),
		ctx:            ctx,
		maxSize:        maxSize,
		isComplete:     true, // Assume complete unless error occurs
	}

	// Pre-allocate builder capacity if we have a content-length hint
	if cl := w.Header().Get("Content-Length"); cl != "" {
		// Content-Length hint available, pre-allocate
		rc.body.Grow(len(cl) * 100) // Heuristic: typical SSE response size
	}

	return rc
}

// SetCompletionCallback sets a callback to be invoked when the stream completes
// The callback is guaranteed to be called exactly once
func (rc *ResponseCapturer) SetCompletionCallback(callback func()) {
	rc.onComplete = callback
}

// StartMonitoring begins monitoring for stream completion conditions
// Should be called in a separate goroutine for streaming responses
func (rc *ResponseCapturer) StartMonitoring() {
	// Panic recovery to prevent monitoring goroutine crashes
	defer func() {
		if rec := recover(); rec != nil {
			rc.errorMsg = "MONITORING_PANIC"
			rc.isComplete = false
			rc.finalize()
		}
	}()

	if rc.ctx == nil {
		return // Not a streaming response
	}

	// Wait for context cancellation (client disconnect or timeout)
	<-rc.ctx.Done()

	// Determine the reason for context cancellation
	switch rc.ctx.Err() {
	case context.DeadlineExceeded:
		rc.errorMsg = "STREAM_TIMEOUT"
		rc.isComplete = false
	case context.Canceled:
		rc.errorMsg = "CLIENT_DISCONNECT"
		rc.isComplete = false
	default:
		// Unknown context error
		if rc.ctx.Err() != nil {
			rc.errorMsg = "CONTEXT_ERROR: " + rc.ctx.Err().Error()
			rc.isComplete = false
		}
	}

	// Finalize the response
	rc.finalize()
}

// WriteHeader captures the status code and headers
func (rc *ResponseCapturer) WriteHeader(statusCode int) {
	rc.statusCode = statusCode

	// Capture headers
	for k, v := range rc.ResponseWriter.Header() {
		rc.headers[k] = v
	}

	// Forward to original writer
	rc.ResponseWriter.WriteHeader(statusCode)
}

// Write captures response body while writing to the client
// This is called for both regular responses and streaming responses
func (rc *ResponseCapturer) Write(data []byte) (int, error) {
	// Forward to client first (maintain transparency)
	n, err := rc.ResponseWriter.Write(data)

	// Track write errors
	if err != nil {
		rc.writeErr = err
		rc.isComplete = false
		rc.errorMsg = "WRITE_ERROR: " + err.Error()
		// Finalize on error
		rc.finalize()
		return n, err
	}

	// Capture the data if we haven't exceeded the buffer limit
	if rc.maxSize < 0 || rc.currentSize < rc.maxSize {
		// Calculate how much we can still capture
		remaining := int64(len(data))
		if rc.maxSize > 0 {
			remaining = min(remaining, rc.maxSize-rc.currentSize)
		}

		if remaining > 0 {
			// Capture the data (or portion of it)
			rc.body.Write(data[:remaining])
			rc.currentSize += int64(len(data))

			// Check if we just exceeded the limit
			if rc.maxSize > 0 && rc.currentSize >= rc.maxSize {
				rc.truncated = true
			}
		}
	} else {
		// We're beyond the limit, just track the size
		rc.currentSize += int64(len(data))
	}

	return n, nil
}

// Flush implements http.Flusher for streaming support
// Required for Server-Sent Events (SSE)
func (rc *ResponseCapturer) Flush() {
	if flusher, ok := rc.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// finalize calls the completion callback exactly once
func (rc *ResponseCapturer) finalize() {
	// Use atomic CAS to ensure callback is called exactly once
	if rc.completed.CompareAndSwap(false, true) {
		if rc.onComplete != nil {
			rc.onComplete()
		}
	}
}

// Complete signals that the response is complete and triggers finalization
// Can be called multiple times safely (callback invoked only once)
func (rc *ResponseCapturer) Complete() {
	rc.finalize()
}

// StatusCode returns the captured status code
func (rc *ResponseCapturer) StatusCode() int {
	return rc.statusCode
}

// Body returns the captured response body
// If truncated, appends a truncation marker
func (rc *ResponseCapturer) Body() string {
	body := rc.body.String()
	if rc.truncated {
		body += "\n[TRUNCATED: response exceeded max_audit_body_size limit]"
	}
	return body
}

// Headers returns the captured headers
func (rc *ResponseCapturer) Headers() http.Header {
	return rc.headers
}

// Error returns the error message if the response was incomplete
// Empty string indicates no error
func (rc *ResponseCapturer) Error() string {
	return rc.errorMsg
}

// IsComplete returns whether the response body is complete
func (rc *ResponseCapturer) IsComplete() bool {
	return rc.isComplete
}

// IsTruncated returns whether the response body was truncated
func (rc *ResponseCapturer) IsTruncated() bool {
	return rc.truncated
}

// TruncatedAtBytes returns the original size before truncation
// Only meaningful when IsTruncated() returns true
func (rc *ResponseCapturer) TruncatedAtBytes() int64 {
	return rc.currentSize
}
