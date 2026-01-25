package proxy

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestReconstructStreamResponse(t *testing.T) {
	// Simulated SSE stream from OpenAI
	sseStream := `data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4","service_tier":"default","system_fingerprint":"fp_abc","choices":[{"index":0,"delta":{"role":"assistant","content":"","refusal":null},"logprobs":null,"finish_reason":null}]}

data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4","service_tier":"default","system_fingerprint":"fp_abc","choices":[{"index":0,"delta":{"content":"Hello"},"logprobs":null,"finish_reason":null}]}

data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4","service_tier":"default","system_fingerprint":"fp_abc","choices":[{"index":0,"delta":{"content":" world"},"logprobs":null,"finish_reason":null}]}

data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4","service_tier":"default","system_fingerprint":"fp_abc","choices":[{"index":0,"delta":{"content":"!"},"logprobs":null,"finish_reason":null}]}

data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4","service_tier":"default","system_fingerprint":"fp_abc","choices":[{"index":0,"delta":{},"logprobs":null,"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":3,"total_tokens":13}}

data: [DONE]

`

	startTime := time.Now()
	reconstructed, metadata := reconstructStreamResponse(sseStream, startTime)

	// Verify reconstruction succeeded
	if reconstructed == "" {
		t.Fatal("Reconstruction failed: empty result")
	}

	// Verify it's valid JSON
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(reconstructed), &result); err != nil {
		t.Fatalf("Reconstructed response is not valid JSON: %v\nGot: %s", err, reconstructed)
	}

	// Verify metadata
	if metadata == nil {
		t.Fatal("Expected metadata to be present")
	}
	if metadata.ChunksReceived != 5 {
		t.Errorf("Expected 5 chunks, got %d", metadata.ChunksReceived)
	}
	if !metadata.ReconstructedFromStream {
		t.Error("Expected ReconstructedFromStream to be true")
	}

	// Verify content was concatenated correctly
	choices, ok := result["choices"].([]interface{})
	if !ok || len(choices) == 0 {
		t.Fatal("No choices in reconstructed response")
	}

	choice := choices[0].(map[string]interface{})
	message := choice["message"].(map[string]interface{})
	content, ok := message["content"].(string)
	if !ok {
		t.Fatal("No content in reconstructed message")
	}

	expectedContent := "Hello world!"
	if content != expectedContent {
		t.Errorf("Expected content %q, got %q", expectedContent, content)
	}

	// Verify metadata fields
	if result["id"] != "chatcmpl-123" {
		t.Errorf("Expected id 'chatcmpl-123', got %v", result["id"])
	}
	if result["model"] != "gpt-4" {
		t.Errorf("Expected model 'gpt-4', got %v", result["model"])
	}
	if result["object"] != "chat.completion" {
		t.Errorf("Expected object 'chat.completion', got %v", result["object"])
	}

	// Verify usage
	usage, ok := result["usage"].(map[string]interface{})
	if !ok {
		t.Fatal("No usage in reconstructed response")
	}
	if usage["total_tokens"] != float64(13) {
		t.Errorf("Expected total_tokens 13, got %v", usage["total_tokens"])
	}

	// Verify finish reason
	if choice["finish_reason"] != "stop" {
		t.Errorf("Expected finish_reason 'stop', got %v", choice["finish_reason"])
	}

	t.Logf("✓ Stream reconstruction successful")
	t.Logf("  Original SSE size: %d bytes", len(sseStream))
	t.Logf("  Reconstructed size: %d bytes", len(reconstructed))
	t.Logf("  Size reduction: %.1f%%", (1-float64(len(reconstructed))/float64(len(sseStream)))*100)
}

func TestReconstructStreamWithToolCalls(t *testing.T) {
	// SSE stream with tool calls
	sseStream := `data: {"id":"chatcmpl-456","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":null,"tool_calls":[{"id":"call_abc","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"London\"}"}}]},"finish_reason":"tool_calls"}]}

data: [DONE]

`

	startTime := time.Now()
	reconstructed, metadata := reconstructStreamResponse(sseStream, startTime)

	if reconstructed == "" {
		t.Fatal("Reconstruction failed")
	}

	var result map[string]interface{}
	if err := json.Unmarshal([]byte(reconstructed), &result); err != nil {
		t.Fatalf("Invalid JSON: %v", err)
	}

	// Verify tool calls
	choices := result["choices"].([]interface{})
	choice := choices[0].(map[string]interface{})
	message := choice["message"].(map[string]interface{})

	// Content should be null for tool calls
	if message["content"] != nil {
		t.Errorf("Expected content to be null for tool calls, got %v", message["content"])
	}

	// Verify tool calls exist
	toolCalls, ok := message["tool_calls"].([]interface{})
	if !ok || len(toolCalls) == 0 {
		t.Fatal("No tool calls in reconstructed response")
	}

	if metadata.ChunksReceived != 1 {
		t.Errorf("Expected 1 chunk, got %d", metadata.ChunksReceived)
	}

	t.Logf("✓ Tool call reconstruction successful")
}

func TestReconstructStreamNonSSEFormat(t *testing.T) {
	// Non-SSE content should be returned as-is
	nonSSE := `{"regular":"json","response":true}`

	startTime := time.Now()
	reconstructed, metadata := reconstructStreamResponse(nonSSE, startTime)

	if reconstructed != nonSSE {
		t.Error("Non-SSE content should be returned unchanged")
	}

	if metadata != nil {
		t.Error("Non-SSE content should not have metadata")
	}

	t.Logf("✓ Non-SSE content handled correctly")
}

func TestParseSSEChunks(t *testing.T) {
	sseData := `data: {"test":1}

data: {"test":2}

data: [DONE]

`

	chunks := parseSSEChunks(sseData)

	if len(chunks) != 2 {
		t.Errorf("Expected 2 chunks (excluding [DONE]), got %d", len(chunks))
	}

	if chunks[0].data["test"] != float64(1) {
		t.Error("First chunk not parsed correctly")
	}
	if chunks[1].data["test"] != float64(2) {
		t.Error("Second chunk not parsed correctly")
	}

	t.Logf("✓ SSE parsing successful")
}

func TestReconstructStreamSizeComparison(t *testing.T) {
	// Create a realistic stream with many chunks
	var sseBuilder strings.Builder
	for i := 0; i < 50; i++ {
		sseBuilder.WriteString(`data: {"id":"chatcmpl-test","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4-turbo","service_tier":"default","system_fingerprint":"fp_test123","choices":[{"index":0,"delta":{"content":"word"},"logprobs":null,"finish_reason":null}]}

`)
	}
	sseBuilder.WriteString(`data: [DONE]

`)

	startTime := time.Now()
	reconstructed, metadata := reconstructStreamResponse(sseBuilder.String(), startTime)

	if metadata == nil {
		t.Fatal("Expected metadata for SSE stream")
	}

	originalSize := len(sseBuilder.String())
	reconstructedSize := len(reconstructed)
	reduction := (1 - float64(reconstructedSize)/float64(originalSize)) * 100

	t.Logf("✓ Size comparison:")
	t.Logf("  Original SSE: %d bytes", originalSize)
	t.Logf("  Reconstructed: %d bytes", reconstructedSize)
	t.Logf("  Reduction: %.1f%%", reduction)
	t.Logf("  Chunks: %d", metadata.ChunksReceived)

	if reduction < 50 {
		t.Errorf("Expected at least 50%% size reduction, got %.1f%%", reduction)
	}
}
