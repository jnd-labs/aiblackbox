package proxy

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jnd-labs/aiblackbox/internal/audit"
	"github.com/jnd-labs/aiblackbox/internal/config"
	"github.com/jnd-labs/aiblackbox/internal/media"
	"github.com/jnd-labs/aiblackbox/internal/models"
	"github.com/jnd-labs/aiblackbox/internal/trace"
)

// Handler implements the reverse proxy with named endpoint routing and audit logging
type Handler struct {
	config         *config.Config
	auditWorker    *audit.Worker
	mediaExtractor *media.Extractor
	nextSequenceID uint64 // Atomic counter for sequence IDs
}

// NewHandler creates a new proxy handler
func NewHandler(cfg *config.Config, auditWorker *audit.Worker) *Handler {
	// Initialize media extractor
	mediaExtractor := media.NewExtractor(
		cfg.Media.EnableExtraction,
		cfg.Media.MinSizeKB,
		cfg.Media.StoragePath,
	)

	return &Handler{
		config:         cfg,
		auditWorker:    auditWorker,
		mediaExtractor: mediaExtractor,
	}
}

// ServeHTTP implements http.Handler interface
// Routes requests based on the first path segment (endpoint name)
// Format: /{endpoint_name}/{actual_path}
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Panic recovery to ensure proxy remains operational
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("PANIC: Recovered from panic in ServeHTTP: %v", rec)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
	}()

	startTime := time.Now()

	// Parse the endpoint name from the URL path
	endpointName, actualPath := h.parseEndpoint(r.URL.Path)
	if endpointName == "" {
		http.Error(w, "Invalid request: endpoint name is required (format: /{endpoint_name}/{path})", http.StatusBadRequest)
		return
	}

	// Lookup endpoint configuration
	endpoint, found := h.config.GetEndpoint(endpointName)
	if !found {
		http.Error(w, fmt.Sprintf("Unknown endpoint: %s", endpointName), http.StatusNotFound)
		return
	}

	// Parse target URL
	targetURL, err := url.Parse(endpoint.Target)
	if err != nil {
		log.Printf("ERROR: Invalid target URL for endpoint %s: %v", endpointName, err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Read and capture request body
	requestBody, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("ERROR: Failed to read request body for endpoint %s: %v", endpointName, err)
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	r.Body.Close()

	// Replace body with a new reader for the proxy
	r.Body = io.NopCloser(bytes.NewReader(requestBody))

	// Create reverse proxy
	proxy := httputil.NewSingleHostReverseProxy(targetURL)

	// Customize the director to modify the request
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		// Combine target's base path with the actual request path
		req.URL.Path = singleJoiningSlash(targetURL.Path, actualPath)
		req.Host = targetURL.Host
	}

	// Check if this is a streaming request (SSE)
	isStreaming := strings.Contains(r.Header.Get("Accept"), "text/event-stream") ||
		strings.Contains(r.Header.Get("Content-Type"), "text/event-stream")

	if isStreaming && h.config.Streaming.EnableSequenceTracking {
		// Handle streaming response with deferred audit finalization
		h.handleStreamingResponse(w, r, proxy, startTime, endpointName, actualPath, requestBody)
	} else {
		// Handle regular response with immediate audit finalization
		h.handleRegularResponse(w, r, proxy, startTime, endpointName, actualPath, requestBody, isStreaming)
	}
}

// parseEndpoint extracts the endpoint name and actual path from the request path
// Example: "/openai/chat/completions" -> ("openai", "/chat/completions")
func (h *Handler) parseEndpoint(path string) (string, string) {
	// Remove leading slash
	path = strings.TrimPrefix(path, "/")

	// Split on first slash
	parts := strings.SplitN(path, "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		return "", ""
	}

	endpointName := parts[0]
	actualPath := "/"
	if len(parts) == 2 {
		actualPath = "/" + parts[1]
	}

	return endpointName, actualPath
}

// singleJoiningSlash joins two URL paths with a single slash
// Handles cases where either path has or doesn't have trailing/leading slashes
func singleJoiningSlash(a, b string) string {
	aslash := strings.HasSuffix(a, "/")
	bslash := strings.HasPrefix(b, "/")
	switch {
	case aslash && bslash:
		return a + b[1:]
	case !aslash && !bslash:
		return a + "/" + b
	}
	return a + b
}

// sensitiveHeaders lists headers that should be masked in audit logs
var sensitiveHeaders = map[string]bool{
	"authorization":       true,
	"cookie":              true,
	"set-cookie":          true,
	"x-api-key":           true,
	"api-key":             true,
	"x-auth-token":        true,
	"x-csrf-token":        true,
	"proxy-authorization": true,
}

// cloneHeaders creates a copy of HTTP headers
func (h *Handler) cloneHeaders(headers http.Header) map[string][]string {
	clone := make(map[string][]string, len(headers))
	for k, v := range headers {
		clone[k] = append([]string(nil), v...)
	}
	return clone
}

// sanitizeHeaders masks sensitive header values for audit logging
// Preserves header structure but redacts sensitive data like bearer tokens
func (h *Handler) sanitizeHeaders(headers map[string][]string) map[string][]string {
	sanitized := make(map[string][]string, len(headers))
	for k, values := range headers {
		lowerKey := strings.ToLower(k)
		if sensitiveHeaders[lowerKey] {
			// Mask sensitive headers but preserve structure
			masked := make([]string, len(values))
			for i, v := range values {
				masked[i] = h.maskSensitiveValue(v)
			}
			sanitized[k] = masked
		} else {
			// Copy non-sensitive headers as-is
			sanitized[k] = append([]string(nil), values...)
		}
	}
	return sanitized
}

// sanitizeResponseHeaders sanitizes response headers and removes Content-Encoding
// if the body was decompressed for audit logging
func (h *Handler) sanitizeResponseHeaders(headers map[string][]string, bodyWasDecompressed bool) map[string][]string {
	sanitized := h.sanitizeHeaders(headers)

	// If we decompressed the body for the audit log, remove Content-Encoding
	// to avoid confusion (the audit log body is now decompressed)
	if bodyWasDecompressed {
		delete(sanitized, "Content-Encoding")
	}

	return sanitized
}

// maskSensitiveValue masks a sensitive header value
// Shows prefix and last 4 characters for debugging while hiding the secret
func (h *Handler) maskSensitiveValue(value string) string {
	if len(value) == 0 {
		return "[EMPTY]"
	}

	// For Bearer tokens, mask the token but keep the "Bearer" prefix
	if strings.HasPrefix(value, "Bearer ") || strings.HasPrefix(value, "bearer ") {
		token := value[7:] // Remove "Bearer " prefix
		if len(token) <= 8 {
			return "Bearer [REDACTED]"
		}
		// Show prefix and last 4 chars: "Bearer sk-...abc123"
		return fmt.Sprintf("Bearer %s...%s", token[:3], token[len(token)-4:])
	}

	// For other sensitive values, show only length
	if len(value) <= 8 {
		return "[REDACTED]"
	}

	// Show first 3 and last 4 characters
	return fmt.Sprintf("%s...%s", value[:3], value[len(value)-4:])
}

// getNextSequenceID atomically increments and returns the next sequence ID
func (h *Handler) getNextSequenceID() uint64 {
	return atomic.AddUint64(&h.nextSequenceID, 1) - 1
}

// extractMediaFromBodies extracts Base64 images from request and response bodies
// Returns modified bodies and media references
func (h *Handler) extractMediaFromBodies(requestBody, responseBody string, sequenceID uint64) (
	modifiedReqBody string, reqMedia []models.MediaReference,
	modifiedRespBody string, respMedia []models.MediaReference,
) {
	var err error

	// Extract from request body
	modifiedReqBody, reqMedia, err = h.mediaExtractor.ExtractFromBody(requestBody, sequenceID, "request")
	if err != nil {
		log.Printf("WARNING: Media extraction from request failed: seq=%d, error=%v", sequenceID, err)
		modifiedReqBody = requestBody
		reqMedia = nil
	}

	// Extract from response body
	modifiedRespBody, respMedia, err = h.mediaExtractor.ExtractFromBody(responseBody, sequenceID, "response")
	if err != nil {
		log.Printf("WARNING: Media extraction from response failed: seq=%d, error=%v", sequenceID, err)
		modifiedRespBody = responseBody
		respMedia = nil
	}

	return modifiedReqBody, reqMedia, modifiedRespBody, respMedia
}

// extractTraceContext extracts or generates distributed tracing metadata
// Hybrid approach:
// - If trace headers present: Use them (explicit tracing)
// - If no headers: Auto-generate for transparent tracing
func (h *Handler) extractTraceContext(r *http.Request) *models.TraceContext {
	traceID := r.Header.Get("X-Trace-ID")
	spanID := r.Header.Get("X-Span-ID")
	parentSpanID := r.Header.Get("X-Parent-Span-ID")

	// Auto-generate trace ID if not provided (transparent tracing)
	if traceID == "" {
		traceID = generateTraceID()
		// Don't log for auto-generated - this is normal operation
	}

	// Auto-generate span ID if not provided
	if spanID == "" {
		spanID = generateSpanID()
	}

	return &models.TraceContext{
		TraceID:      traceID,
		SpanID:       spanID,
		ParentSpanID: parentSpanID,
		// SpanType, ToolCall, ToolResult will be populated during response processing
		Attributes: make(map[string]string),
	}
}

// generateTraceID generates a 128-bit (32 hex chars) trace identifier
// Format matches OpenTelemetry specification
func generateTraceID() string {
	bytes := make([]byte, 16) // 128 bits
	if _, err := rand.Read(bytes); err != nil {
		// Fallback to timestamp-based ID if random fails
		log.Printf("WARNING: Failed to generate random trace ID: %v", err)
		return fmt.Sprintf("%032x", time.Now().UnixNano())
	}
	return hex.EncodeToString(bytes)
}

// generateSpanID generates a 64-bit (16 hex chars) span identifier
// Format matches OpenTelemetry specification
func generateSpanID() string {
	bytes := make([]byte, 8) // 64 bits
	if _, err := rand.Read(bytes); err != nil {
		// Fallback to timestamp-based ID if random fails
		log.Printf("WARNING: Failed to generate random span ID: %v", err)
		return fmt.Sprintf("%016x", time.Now().UnixNano())
	}
	return hex.EncodeToString(bytes)
}

// handleRegularResponse handles non-streaming responses with immediate audit finalization
func (h *Handler) handleRegularResponse(
	w http.ResponseWriter,
	r *http.Request,
	proxy *httputil.ReverseProxy,
	startTime time.Time,
	endpointName string,
	actualPath string,
	requestBody []byte,
	isStreaming bool,
) {
	// Create response capturer
	capturer := NewResponseCapturer(w)

	// Proxy the request
	proxy.ServeHTTP(capturer, r)

	// Calculate duration
	duration := time.Since(startTime)

	// Assign sequence ID
	sequenceID := h.getNextSequenceID()

	// Get decompressed response body for audit logging
	responseBody := capturer.DecompressedBody()
	bodyWasDecompressed := responseBody != capturer.Body()

	// Detect and reconstruct streaming responses (SSE format)
	// This handles cases where streaming wasn't detected from request headers
	var streamingMetadata *models.StreamingMetadata
	contentType := capturer.Headers().Get("Content-Type")
	if strings.Contains(contentType, "text/event-stream") {
		reconstructedBody, metadata := reconstructStreamResponse(responseBody, startTime)
		if metadata != nil {
			responseBody = reconstructedBody
			streamingMetadata = metadata
			isStreaming = true // Update flag for audit log
		}
	}

	// Extract media from request and response bodies
	modifiedReqBody, reqMedia, modifiedRespBody, respMedia := h.extractMediaFromBodies(
		string(requestBody),
		responseBody,
		sequenceID,
	)

	// Extract trace context from headers
	traceContext := h.extractTraceContext(r)

	// Enrich trace context with tool call/result detection
	if traceContext != nil {
		trace.EnrichTraceContext(traceContext, string(requestBody), responseBody)
	}

	// Create audit entry with complete data
	entry := &models.AuditEntry{
		Timestamp:  startTime,
		Endpoint:   endpointName,
		SequenceID: sequenceID,
		Request: models.RequestDetails{
			Method:          r.Method,
			Path:            actualPath,
			Headers:         h.sanitizeHeaders(h.cloneHeaders(r.Header)),
			Body:            modifiedReqBody,
			ContentLength:   r.ContentLength,
			MediaReferences: reqMedia,
		},
		Response: models.ResponseDetails{
			StatusCode:        capturer.StatusCode(),
			Headers:           h.sanitizeResponseHeaders(h.cloneHeaders(capturer.Headers()), bodyWasDecompressed),
			Body:              modifiedRespBody,
			ContentLength:     int64(len(responseBody)),
			Duration:          duration,
			IsStreaming:       isStreaming,
			IsComplete:        capturer.IsComplete(),
			Error:             capturer.Error(),
			MediaReferences:   respMedia,
			StreamingMetadata: streamingMetadata,
		},
		Trace: traceContext,
	}

	// Send to audit worker (non-blocking due to buffered channel)
	h.auditWorker.Log(entry)
}

// handleStreamingResponse handles streaming (SSE) responses with deferred audit finalization
func (h *Handler) handleStreamingResponse(
	w http.ResponseWriter,
	r *http.Request,
	proxy *httputil.ReverseProxy,
	startTime time.Time,
	endpointName string,
	actualPath string,
	requestBody []byte,
) {
	// Assign sequence ID immediately (ensures correct ordering)
	sequenceID := h.getNextSequenceID()

	// Create context with timeout for stream monitoring
	streamTimeout := time.Duration(h.config.Streaming.StreamTimeout) * time.Second
	ctx, cancel := context.WithTimeout(r.Context(), streamTimeout)
	defer cancel()

	// Extract trace context from headers (do this before callback closure)
	traceContext := h.extractTraceContext(r)

	// Create streaming response capturer with buffer limits
	capturer := NewStreamingResponseCapturer(w, ctx, h.config.Streaming.MaxAuditBodySize)

	// Set up completion callback for deferred audit finalization
	capturer.SetCompletionCallback(func() {
		// Panic recovery in callback to prevent crashing the worker
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("PANIC: Recovered from panic in streaming finalization callback: endpoint=%s, seq=%d, error=%v",
					endpointName, sequenceID, rec)
			}
		}()

		// Calculate total duration
		duration := time.Since(startTime)

		// Get decompressed response body for audit logging
		responseBody := capturer.DecompressedBody()
		bodyWasDecompressed := responseBody != capturer.Body()

		// Reconstruct streaming response from SSE deltas
		reconstructedBody, streamingMetadata := reconstructStreamResponse(responseBody, startTime)

		// Extract media from request and response bodies
		modifiedReqBody, reqMedia, modifiedRespBody, respMedia := h.extractMediaFromBodies(
			string(requestBody),
			reconstructedBody,
			sequenceID,
		)

		// Enrich trace context with tool call/result detection
		if traceContext != nil {
			trace.EnrichTraceContext(traceContext, string(requestBody), reconstructedBody)
		}

		// Create audit entry with finalized data
		entry := &models.AuditEntry{
			Timestamp:  startTime,
			Endpoint:   endpointName,
			SequenceID: sequenceID,
			Request: models.RequestDetails{
				Method:          r.Method,
				Path:            actualPath,
				Headers:         h.sanitizeHeaders(h.cloneHeaders(r.Header)),
				Body:            modifiedReqBody,
				ContentLength:   r.ContentLength,
				MediaReferences: reqMedia,
			},
			Response: models.ResponseDetails{
				StatusCode:        capturer.StatusCode(),
				Headers:           h.sanitizeResponseHeaders(h.cloneHeaders(capturer.Headers()), bodyWasDecompressed),
				Body:              modifiedRespBody,
				ContentLength:     int64(len(reconstructedBody)),
				Duration:          duration,
				IsStreaming:       true,
				IsComplete:        capturer.IsComplete(),
				Error:             capturer.Error(),
				Truncated:         capturer.IsTruncated(),
				TruncatedAtBytes:  capturer.TruncatedAtBytes(),
				MediaReferences:   respMedia,
				StreamingMetadata: streamingMetadata,
			},
			Trace: traceContext,
		}

		// Send to audit worker
		h.auditWorker.Log(entry)

		// Log media extraction if any
		if len(reqMedia) > 0 || len(respMedia) > 0 {
			log.Printf("INFO: Media extracted: endpoint=%s, seq=%d, request_images=%d, response_images=%d",
				endpointName, sequenceID, len(reqMedia), len(respMedia))
		}

		// Log completion with appropriate level
		if entry.Response.IsComplete {
			log.Printf("INFO: Streaming response completed: endpoint=%s, seq=%d, duration=%v, bytes=%d",
				endpointName, sequenceID, duration, entry.Response.ContentLength)
		} else {
			log.Printf("WARNING: Streaming response incomplete: endpoint=%s, seq=%d, duration=%v, error=%s",
				endpointName, sequenceID, duration, entry.Response.Error)
		}

		// Log truncation if occurred
		if entry.Response.Truncated {
			log.Printf("WARNING: Response body truncated in audit: endpoint=%s, seq=%d, original=%d, limit=%d",
				endpointName, sequenceID, entry.Response.TruncatedAtBytes, h.config.Streaming.MaxAuditBodySize)
		}
	})

	// Start monitoring for stream completion in background
	go capturer.StartMonitoring()

	// Proxy the request (connection stays open for streaming)
	proxy.ServeHTTP(capturer, r)

	// ServeHTTP returns when the upstream finishes or connection breaks
	// Finalize the audit entry (callback called only once due to atomic flag)
	capturer.Complete()
}
