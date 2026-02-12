# AIBlackBox 🛡️🤖
### Immutable Audit Trail for LLM Applications

**AIBlackBox** is a high-performance, transparent proxy built in Go, designed to act as a "Flight Recorder" for your AI interactions. It ensures that every request and response between your application and LLM providers (OpenAI, Azure, Anthropic, Local models) is cryptographically signed, chained, and stored for compliance and auditing.



## ✨ Key Features
- **Transparent Proxying:** No code changes required. Just point your OpenAI-compatible SDK to AIBlackBox.
- **Named Endpoints:** Route requests to multiple providers through a single secure gateway.
- **Cryptographic Integrity:** Uses SHA-256 hash-chaining to ensure logs are tamper-proof and verifiable.
- **Async Processing:** Near-zero latency overhead (<1ms) thanks to Go's concurrency and non-blocking architecture.
- **Streaming Support:** Full support for Server-Sent Events (SSE) with intelligent response reconstruction and sequence tracking for concurrent streams.
- **Security Built-In:** Automatic masking of sensitive headers (Authorization, cookies, API keys) in audit logs.
- **Hybrid Tracing:** Automatic tool call detection and conversation threading without client changes, plus optional explicit tracing headers for sophisticated agentic workflows.
- **Readable Audit Logs:** Automatic gzip decompression and streaming consolidation (87-96% size reduction).
- **Media Extraction:** Automatic extraction of large Base64-encoded images from request/response bodies to separate files, reducing log bloat.
- **Verification Tool:** Built-in hash chain validator to detect any tampering or corruption.
- **Enterprise Ready:** Lightweight Docker image, environment variable configuration, and local volume support.

## 🚀 Quick Start

### 1. Configuration
Create a `config.yaml` to define your endpoints:

```yaml
server:
  port: 8080
  genesis_seed: "firm-exclusive-secret-seed"

endpoints:
  - name: "production"
    target: "https://api.openai.com/v1"
  - name: "research"
    target: "http://internal-llm-server:11434/v1"

storage:
  path: "./logs/audit.jsonl"

streaming:
  max_audit_body_size: 10485760    # 10 MB
  stream_timeout: 300               # 5 minutes
  enable_sequence_tracking: true    # Maintain hash chain with concurrent streams

media:
  enable_extraction: true
  min_size_kb: 100
  storage_path: "./logs/media"
```

### 2. Run with Docker
```bash
docker run -p 8080:8080 \
  -v $(pwd)/config.yaml:/app/config.yaml \
  -v $(pwd)/logs:/app/logs \
  jndlabs/aiblackbox:latest
```

**Note:** The `logs` volume will contain both the audit log (`audit.jsonl`) and extracted media files (`media/`).

### 3. Integration
Simply change the base_url in your application's SDK.

Python Example:

``` Python

from openai import OpenAI

client = OpenAI(
    api_key="your-api-key",
    base_url="http://localhost:8080/production"  # Just change the base_url!
)

# All your existing code works without changes
response = client.chat.completions.create(
    model="gpt-4",
    messages=[{"role": "user", "content": "Hello!"}]
)
```

---

## 🔒 Security Features

### Automatic Header Sanitization
AIBlackBox automatically masks sensitive information in audit logs:

**Protected Headers:**
- `Authorization` (Bearer tokens, API keys)
- `Cookie` / `Set-Cookie`
- `X-Api-Key` / `Api-Key`
- `X-Auth-Token`
- `X-CSRF-Token`
- `Proxy-Authorization`

**Masking Format:**
```
Original: Bearer sk-proj-1234567890abcdefghijklmnop
Stored:   Bearer sk-...mnop
```

This ensures audit logs are safe to store and share without exposing credentials.

---

## 🔍 Distributed Tracing

AIBlackBox implements **hybrid tracing** that works automatically without any client changes, while also supporting sophisticated agentic workflows.

### Automatic Tracing (No Changes Required)

Every request automatically gets:
- **Trace ID**: Unique identifier for tracking
- **Span ID**: Unique per request/response
- **Tool Call Detection**: Automatically detects and links OpenAI function calls
- **Conversation Threading**: Groups related messages via `conversation_id`
- **Span Classification**: TOOL_CALL, TOOL_RESULT, AGENT_THINKING, FINAL_RESPONSE

### Explicit Tracing (Optional)

For multi-step agentic workflows, you can provide trace headers:

```python
from openai import OpenAI
import uuid

def generate_trace_id():
    return uuid.uuid4().hex + uuid.uuid4().hex[:16]

def generate_span_id():
    return uuid.uuid4().hex[:16]

trace_id = generate_trace_id()
client = OpenAI(
    api_key="your-api-key",
    base_url="http://localhost:8080/production",
    default_headers={
        "X-Trace-ID": trace_id,
        "X-Span-ID": generate_span_id()
    }
)

# For linked operations, provide parent span
span1 = generate_span_id()
client.default_headers["X-Span-ID"] = span1
response1 = client.chat.completions.create(...)

span2 = generate_span_id()
client.default_headers["X-Span-ID"] = span2
client.default_headers["X-Parent-Span-ID"] = span1  # Link to previous step
response2 = client.chat.completions.create(...)
```

### Span Types

AIBlackBox automatically classifies each request into one of these span types:

| Span Type | Description | Auto-Detected When |
|-----------|-------------|-------------------|
| `TOOL_CALL` | LLM requests tool execution | Response contains `tool_calls` |
| `TOOL_RESULT` | Tool returns result to LLM | Request contains `role: "tool"` messages |
| `AGENT_THINKING` | LLM processing without tools | Standard chat completion |
| `FINAL_RESPONSE` | Terminal response to user | Response with choices but no tool calls |
| `USER_PROMPT` | Initial user request | First message in conversation |
| `ERROR` | Error occurred | HTTP error status |

This classification enables powerful workflow reconstruction and debugging.

For detailed tracing documentation, see [docs/TRACING.md](docs/TRACING.md)

---

## 🖼️ Media Extraction

AIBlackBox automatically extracts large Base64-encoded images from request and response bodies to prevent log bloat.

### How It Works

When large Base64 images are detected:
1. **Detection**: Scans for `data:image/{type};base64,{data}` patterns
2. **Size Check**: Only extracts images above the configured threshold (default: 100 KB)
3. **Extraction**: Decodes and saves to separate files with SHA-256 hash verification
4. **Placeholder**: Replaces inline data with `[IMAGE_EXTRACTED:0]` reference
5. **Audit Trail**: Stores metadata in `media_references` field

### File Organization

```
logs/media/
├── 2026-02-11/
│   ├── seq_42_request_0.png
│   ├── seq_42_response_0.jpeg
│   ├── seq_43_request_0.png
│   └── seq_43_response_0.webp
└── 2026-02-12/
    └── seq_44_request_0.png
```

### Configuration

```yaml
media:
  enable_extraction: true        # Enable/disable feature
  min_size_kb: 100              # Minimum size to extract (KB)
  storage_path: "./logs/media"  # Where to store extracted files
```

### Media Reference Format

Each extracted image is logged with full metadata:

```json
{
  "request": {
    "media_references": [
      {
        "type": "image/png",
        "file_path": "2026-02-11/seq_42_request_0.png",
        "sha256": "a1b2c3d4e5f6...",
        "size_bytes": 524288,
        "placeholder": "[IMAGE_EXTRACTED:0]"
      }
    ]
  }
}
```

**Benefits:**
- **Reduced Log Size**: 80-95% reduction for image-heavy requests
- **Integrity**: SHA-256 hashes verify file authenticity
- **Organization**: Date-based folder structure for easy management
- **Searchability**: Body text remains searchable without large Base64 blobs

---

## 📊 Querying Audit Logs

Audit logs are stored in JSON Lines format (`logs/audit.jsonl`), making them easy to query with standard tools.

### Find All Requests in a Conversation
```bash
jq 'select(.trace.attributes.conversation_id == "185f8db32271fe25")' logs/audit.jsonl
```

### Find Tool Call and Its Result
```bash
# Find tool call
jq 'select(.trace.span_type == "TOOL_CALL")' logs/audit.jsonl

# Find matching tool result
jq 'select(.trace.tool_result.tool_call_id == "call_abc123")' logs/audit.jsonl
```

### Find All Requests for a Specific Endpoint
```bash
jq 'select(.endpoint == "production")' logs/audit.jsonl
```

### Search Response Bodies
```bash
jq 'select(.response.body | contains("specific text"))' logs/audit.jsonl
```

### Reconstruct Agent Workflow
```bash
jq -r '[.timestamp, .trace.span_id[0:8], .trace.parent_span_id[0:8] // "root", .trace.span_type, .trace.span_name] | @tsv' logs/audit.jsonl | sort
```

### Group Requests by Conversation
```bash
jq -r '.trace.attributes.conversation_id' logs/audit.jsonl | sort | uniq -c
```

### Find Requests with Extracted Media
```bash
jq 'select(.request.media_references != null and (.request.media_references | length) > 0)' logs/audit.jsonl
```

### List All Extracted Media Files
```bash
jq -r '.request.media_references[]?, .response.media_references[]? | .file_path' logs/audit.jsonl | sort -u
```

---

## ✅ Verifying Audit Logs

AIBlackBox includes a verification tool to validate the integrity of your audit logs:

### Basic Verification
```bash
go run ./cmd/verify -file logs/audit.jsonl
```

**Expected Output:**
```
✅ Verification successful!
   Total entries verified: 1234
   Chain integrity: INTACT
   Data integrity: VERIFIED
```

### Verbose Mode
```bash
go run ./cmd/verify -file logs/audit.jsonl -verbose
```

Shows verification status for each log entry.

### What It Checks
- ✅ Hash chain continuity (each `prev_hash` matches previous entry's `hash`)
- ✅ Data integrity (recalculates hash to detect tampering)
- ✅ Cryptographic linking (including all trace context)

### Exit Codes
- `0` - Verification successful
- `1` - File error
- `2` - Chain broken
- `3` - Data tampered
- `4` - Parse error

---

## 🎯 Advanced Features

### Streaming Response Reconstruction

AIBlackBox automatically consolidates Server-Sent Events (SSE) streams into readable JSON:

**Before (Raw SSE):**
```
data: {"id":"chatcmpl-123","object":"chat.completion.chunk","choices":[...]}
data: {"id":"chatcmpl-123","object":"chat.completion.chunk","choices":[...]}
data: {"id":"chatcmpl-123","object":"chat.completion.chunk","choices":[...]}
... (66x redundant metadata)
```

**After (Consolidated):**
```json
{
  "id": "chatcmpl-123",
  "object": "chat.completion",
  "choices": [{
    "message": {
      "role": "assistant",
      "content": "Complete accumulated content"
    }
  }],
  "usage": {...}
}
```

**Result:** 87-96% size reduction while maintaining full searchability.

### Concurrent Stream Handling

AIBlackBox includes **sequence tracking** to maintain hash chain integrity when multiple streams complete out of order:

- Each request receives a sequence ID at the start
- Audit entries are finalized asynchronously when streams complete
- Hash chain remains unbroken regardless of completion order
- Configurable via `streaming.enable_sequence_tracking`

This ensures cryptographic integrity even under high concurrent load.

### Response Body Decompression

All gzip-compressed responses are automatically decompressed before storage:
- Detects `Content-Encoding: gzip` header
- Validates gzip magic bytes (`\x1f\x8b`)
- Stores readable JSON instead of binary data
- 99% of responses are now human-readable

---

## 📁 Audit Log Format

Each audit entry contains:

```json
{
  "timestamp": "2026-02-11T18:00:00Z",
  "endpoint": "production",
  "sequence_id": 42,
  "request": {
    "method": "POST",
    "path": "/chat/completions",
    "headers": {
      "authorization": ["Bearer sk-...LpsA"],
      "content-type": ["application/json"]
    },
    "body": "{\"model\":\"gpt-4\",\"messages\":[...]}",
    "content_length": 1234,
    "media_references": [
      {
        "type": "image/png",
        "file_path": "2026-02-11/seq_42_request_0.png",
        "sha256": "a1b2c3...",
        "size_bytes": 524288,
        "placeholder": "[IMAGE_EXTRACTED:0]"
      }
    ]
  },
  "response": {
    "status_code": 200,
    "headers": {...},
    "body": "{\"id\":\"chatcmpl-123\",\"choices\":[...]}",
    "duration_ms": 1234,
    "content_length": 5678,
    "is_streaming": true,
    "is_complete": true,
    "media_references": [],
    "streaming_metadata": {
      "chunks_received": 45,
      "reconstructed_from_stream": true,
      "first_chunk_ms": 150,
      "last_chunk_ms": 1200
    }
  },
  "trace": {
    "trace_id": "4bf92f3577b34da6a3ce929d0e0e4736",
    "span_id": "00f067aa0ba902b7",
    "parent_span_id": "",
    "span_type": "TOOL_CALL",
    "span_name": "get_weather",
    "tool_call": {
      "id": "call_abc123",
      "type": "function",
      "function": {
        "name": "get_weather",
        "arguments": "{\"location\":\"San Francisco\"}",
        "arguments_hash": "sha256:..."
      },
      "index": 0
    },
    "attributes": {
      "tool_name": "get_weather",
      "tool_call_id": "call_abc123",
      "conversation_id": "185f8db32271fe25",
      "message_count": "4",
      "multi_turn": "true",
      "detection": "auto"
    }
  },
  "prev_hash": "a1b2c3d4...",
  "hash": "f1e2d3c4..."
}
```

---

## 🐳 Docker Deployment

### Using Docker Compose
```yaml
version: '3.8'
services:
  aiblackbox:
    image: jndlabs/aiblackbox:latest
    ports:
      - "8080:8080"
    volumes:
      - ./config.yaml:/app/config.yaml
      - ./logs:/app/logs
    environment:
      - ABB_SERVER_PORT=8080
      - ABB_STORAGE_PATH=/app/logs/audit.jsonl
      - ABB_MEDIA_ENABLE_EXTRACTION=true
      - ABB_MEDIA_STORAGE_PATH=/app/logs/media
```

### Building from Source
```bash
docker build -t jndlabs/aiblackbox:latest .
docker run -p 8080:8080 \
  -v $(pwd)/config.yaml:/app/config.yaml \
  -v $(pwd)/logs:/app/logs \
  jndlabs/aiblackbox:latest
```

## 🤝 Contributing

Contributions are welcome! Please see our contribution guidelines.

---

**Built with ❤️ in Go | Ensuring AI Transparency & Accountability**