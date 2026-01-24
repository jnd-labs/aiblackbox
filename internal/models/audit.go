package models

import "time"

// SpanType represents the type of span in an agentic workflow
type SpanType string

const (
	// SpanTypeUserPrompt: Initial user input to the agent
	SpanTypeUserPrompt SpanType = "USER_PROMPT"

	// SpanTypeAgentThinking: LLM reasoning/planning phase
	SpanTypeAgentThinking SpanType = "AGENT_THINKING"

	// SpanTypeToolCall: Agent requests a tool execution
	SpanTypeToolCall SpanType = "TOOL_CALL"

	// SpanTypeToolResult: Result returned from tool execution
	SpanTypeToolResult SpanType = "TOOL_RESULT"

	// SpanTypeFinalResponse: Agent's final response to user
	SpanTypeFinalResponse SpanType = "FINAL_RESPONSE"

	// SpanTypeError: Error occurred during workflow
	SpanTypeError SpanType = "ERROR"
)

// TraceContext provides distributed tracing metadata for reconstructing agentic workflows
type TraceContext struct {
	// TraceID is the unique identifier for the entire user session or conversation
	// Format: 32-character hex string (128-bit UUID)
	// Example: "4bf92f3577b34da6a3ce929d0e0e4736"
	// Propagated via X-Trace-ID header or auto-generated
	TraceID string `json:"trace_id,omitempty"`

	// SpanID is the unique identifier for this specific request/response pair
	// Format: 16-character hex string (64-bit)
	// Example: "00f067aa0ba902b7"
	SpanID string `json:"span_id,omitempty"`

	// ParentSpanID references the SpanID that triggered this request
	// Empty for root spans (initial user prompts)
	ParentSpanID string `json:"parent_span_id,omitempty"`

	// SpanType categorizes the role of this span in the workflow
	SpanType SpanType `json:"span_type,omitempty"`

	// SpanName is a human-readable description
	// Examples: "user_prompt", "get_weather_tool", "final_response"
	SpanName string `json:"span_name,omitempty"`

	// ToolCall contains structured tool calling information (OpenAI format)
	ToolCall *ToolCallInfo `json:"tool_call,omitempty"`

	// ToolResult contains structured tool result information
	ToolResult *ToolResultInfo `json:"tool_result,omitempty"`

	// Attributes contains additional span metadata
	Attributes map[string]string `json:"attributes,omitempty"`
}

// ToolCallInfo represents a tool invocation by the agent (OpenAI format)
type ToolCallInfo struct {
	// ID is the unique identifier for this tool call
	// Format: OpenAI "call_" prefix (e.g., "call_abc123")
	ID string `json:"id"`

	// Type is the tool call type (typically "function")
	Type string `json:"type"`

	// Function contains the function call details
	Function FunctionCall `json:"function"`

	// Index is the position in the tool_calls array (for multiple parallel calls)
	Index int `json:"index,omitempty"`
}

// FunctionCall represents a function invocation
type FunctionCall struct {
	// Name is the function/tool name
	// Example: "get_weather", "web_search"
	Name string `json:"name"`

	// Arguments is the JSON-encoded function arguments
	// Example: "{\"city\": \"London\", \"units\": \"celsius\"}"
	Arguments string `json:"arguments"`

	// ArgumentsHash is SHA256(Arguments) for integrity verification
	ArgumentsHash string `json:"arguments_hash"`
}

// ToolResultInfo represents the result of a tool execution
type ToolResultInfo struct {
	// ToolCallID links this result to the tool call that triggered it
	// Must match ToolCallInfo.ID from the parent TOOL_CALL span
	ToolCallID string `json:"tool_call_id"`

	// Content is the tool's output
	Content string `json:"content"`

	// ContentHash is SHA256(Content) for integrity verification
	ContentHash string `json:"content_hash"`

	// IsError indicates if the tool execution failed
	IsError bool `json:"is_error,omitempty"`

	// ErrorMessage contains error details if IsError is true
	ErrorMessage string `json:"error_message,omitempty"`
}

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

	// Trace contains distributed tracing metadata for agentic workflows
	// Optional field - maintains backward compatibility when omitted
	Trace *TraceContext `json:"trace,omitempty"`
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
