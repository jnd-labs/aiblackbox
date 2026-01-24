package proxy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/aiblackbox/proxy/internal/audit"
	"github.com/aiblackbox/proxy/internal/config"
	"github.com/aiblackbox/proxy/internal/models"
)

// Handler implements the reverse proxy with named endpoint routing and audit logging
type Handler struct {
	config         *config.Config
	auditWorker    *audit.Worker
	nextSequenceID uint64 // Atomic counter for sequence IDs
}

// NewHandler creates a new proxy handler
func NewHandler(cfg *config.Config, auditWorker *audit.Worker) *Handler {
	return &Handler{
		config:      cfg,
		auditWorker: auditWorker,
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
		req.URL.Path = actualPath
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

// cloneHeaders creates a copy of HTTP headers
func (h *Handler) cloneHeaders(headers http.Header) map[string][]string {
	clone := make(map[string][]string, len(headers))
	for k, v := range headers {
		clone[k] = append([]string(nil), v...)
	}
	return clone
}

// getNextSequenceID atomically increments and returns the next sequence ID
func (h *Handler) getNextSequenceID() uint64 {
	return atomic.AddUint64(&h.nextSequenceID, 1) - 1
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

	// Create audit entry with complete data
	entry := &models.AuditEntry{
		Timestamp:  startTime,
		Endpoint:   endpointName,
		SequenceID: sequenceID,
		Request: models.RequestDetails{
			Method:        r.Method,
			Path:          actualPath,
			Headers:       h.cloneHeaders(r.Header),
			Body:          string(requestBody),
			ContentLength: r.ContentLength,
		},
		Response: models.ResponseDetails{
			StatusCode:    capturer.StatusCode(),
			Headers:       h.cloneHeaders(capturer.Headers()),
			Body:          capturer.Body(),
			ContentLength: int64(len(capturer.Body())),
			Duration:      duration,
			IsStreaming:   isStreaming,
			IsComplete:    capturer.IsComplete(),
			Error:         capturer.Error(),
		},
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

		// Create audit entry with finalized data
		entry := &models.AuditEntry{
			Timestamp:  startTime,
			Endpoint:   endpointName,
			SequenceID: sequenceID,
			Request: models.RequestDetails{
				Method:        r.Method,
				Path:          actualPath,
				Headers:       h.cloneHeaders(r.Header),
				Body:          string(requestBody),
				ContentLength: r.ContentLength,
			},
			Response: models.ResponseDetails{
				StatusCode:    capturer.StatusCode(),
				Headers:       h.cloneHeaders(capturer.Headers()),
				Body:          capturer.Body(),
				ContentLength: int64(len(capturer.Body())),
				Duration:      duration,
				IsStreaming:   true,
				IsComplete:    capturer.IsComplete(),
				Error:         capturer.Error(),
				Truncated:     capturer.IsTruncated(),
				TruncatedAtBytes: capturer.TruncatedAtBytes(),
			},
		}

		// Send to audit worker
		h.auditWorker.Log(entry)

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
