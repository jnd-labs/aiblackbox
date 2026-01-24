package models

import "time"

// MediaReference represents an extracted media file that was offloaded from the audit log
type MediaReference struct {
	// Type is the media type (e.g., "image/png", "image/jpeg")
	Type string `json:"type"`

	// FilePath is the relative path to the extracted media file
	// Example: "logs/media/2026-01-24/seq_0_request_0.png"
	FilePath string `json:"file_path"`

	// SHA256 is the SHA-256 hash of the original Base64-encoded content
	// Used for integrity verification
	SHA256 string `json:"sha256"`

	// SizeBytes is the size of the decoded media file in bytes
	SizeBytes int64 `json:"size_bytes"`

	// Placeholder is the string that replaced the Base64 content in the body
	// Example: "[IMAGE_EXTRACTED:0]"
	Placeholder string `json:"placeholder"`
}

// AuditEntry represents a single audit log entry with cryptographic integrity.
// Each entry is chained to the previous one via SHA-256 hashing.
type AuditEntry struct {
	// Timestamp is the exact time when the request was received (RFC3339 format)
	Timestamp time.Time `json:"timestamp"`

	// Endpoint is the named endpoint from config (e.g., "openai", "local")
	Endpoint string `json:"endpoint"`

	// Request contains details about the incoming request
	Request RequestDetails `json:"request"`

	// Response contains details about the proxied response
	Response ResponseDetails `json:"response"`

	// SequenceID is a monotonically increasing sequence number assigned at request time
	// Used to maintain correct hash chain order even when streaming responses complete out of order
	SequenceID uint64 `json:"sequence_id"`

	// PrevHash is the SHA-256 hash of the previous audit entry
	// For the first entry, this is derived from the genesis_seed
	PrevHash string `json:"prev_hash"`

	// Hash is the SHA-256 hash of this entry
	// Computed as: SHA256(Timestamp + Endpoint + RequestBody + ResponseBody + StatusCode + PrevHash)
	Hash string `json:"hash"`
}

// RequestDetails captures all relevant information about the incoming request
type RequestDetails struct {
	// Method is the HTTP method (GET, POST, etc.)
	Method string `json:"method"`

	// Path is the URL path after stripping the endpoint name
	Path string `json:"path"`

	// Headers contains all HTTP headers (sensitive headers like Authorization are included)
	Headers map[string][]string `json:"headers"`

	// Body is the raw request body (typically JSON for LLM APIs)
	// Large Base64 images may be replaced with placeholders if media extraction is enabled
	Body string `json:"body"`

	// ContentLength is the size of the request body in bytes
	ContentLength int64 `json:"content_length"`

	// MediaReferences contains information about extracted media files
	// Populated when large Base64 images are detected and offloaded to separate storage
	MediaReferences []MediaReference `json:"media_references,omitempty"`
}

// ResponseDetails captures all relevant information about the proxied response
type ResponseDetails struct {
	// StatusCode is the HTTP status code (200, 404, 500, etc.)
	StatusCode int `json:"status_code"`

	// Headers contains all HTTP response headers
	Headers map[string][]string `json:"headers"`

	// Body is the complete response body (captured even during streaming)
	Body string `json:"body"`

	// ContentLength is the size of the response body in bytes
	ContentLength int64 `json:"content_length"`

	// Duration is how long the proxied request took to complete
	Duration time.Duration `json:"duration_ms"`

	// IsStreaming indicates if this was a Server-Sent Events (SSE) response
	IsStreaming bool `json:"is_streaming"`

	// Error indicates if the response was incomplete or errored
	// Examples: "CLIENT_DISCONNECT", "UPSTREAM_ERROR: connection reset", "STREAM_TIMEOUT"
	// Empty string means no error occurred
	Error string `json:"error,omitempty"`

	// IsComplete indicates if the response body is complete
	// False if stream was interrupted before natural completion
	IsComplete bool `json:"is_complete"`

	// Truncated indicates if the response body was truncated due to size limits
	// Only applicable when body exceeds max_audit_body_size configuration
	Truncated bool `json:"truncated,omitempty"`

	// TruncatedAtBytes indicates the original size before truncation
	// Only set when Truncated is true
	TruncatedAtBytes int64 `json:"truncated_at_bytes,omitempty"`

	// MediaReferences contains information about extracted media files
	// Populated when large Base64 images are detected and offloaded to separate storage
	MediaReferences []MediaReference `json:"media_references,omitempty"`
}
