package proxy

import (
	"encoding/json"
	"log"
	"strings"
	"time"

	"github.com/jnd-labs/aiblackbox/internal/models"
)

// reconstructStreamResponse converts SSE stream format into a consolidated response
// Parses OpenAI streaming format and rebuilds the complete response
func reconstructStreamResponse(sseBody string, startTime time.Time) (string, *models.StreamingMetadata) {
	// Parse SSE stream into chunks
	chunks := parseSSEChunks(sseBody)
	if len(chunks) == 0 {
		// Not SSE format or empty, return as-is
		return sseBody, nil
	}

	// Reconstruct the final response from deltas
	reconstructed, metadata := reconstructOpenAIStream(chunks, startTime)
	if reconstructed == "" {
		// Reconstruction failed, return original
		return sseBody, nil
	}

	return reconstructed, metadata
}

// sseChunk represents a parsed SSE data chunk
type sseChunk struct {
	data      map[string]interface{}
	timestamp time.Time
}

// parseSSEChunks parses SSE format into structured chunks
func parseSSEChunks(body string) []sseChunk {
	var chunks []sseChunk
	lines := strings.Split(body, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)

		// Skip empty lines and [DONE] marker
		if line == "" || line == "data: [DONE]" {
			continue
		}

		// Parse SSE data lines
		if strings.HasPrefix(line, "data: ") {
			jsonData := strings.TrimPrefix(line, "data: ")

			var data map[string]interface{}
			if err := json.Unmarshal([]byte(jsonData), &data); err != nil {
				log.Printf("WARNING: Failed to parse SSE chunk: %v", err)
				continue
			}

			chunks = append(chunks, sseChunk{
				data:      data,
				timestamp: time.Now(), // Approximate timing
			})
		}
	}

	return chunks
}

// reconstructOpenAIStream rebuilds OpenAI streaming response from deltas
func reconstructOpenAIStream(chunks []sseChunk, startTime time.Time) (string, *models.StreamingMetadata) {
	if len(chunks) == 0 {
		return "", nil
	}

	// Use first chunk as template for metadata
	firstChunk := chunks[0].data

	// Build the reconstructed response
	reconstructed := make(map[string]interface{})

	// Copy metadata from first chunk
	if id, ok := firstChunk["id"].(string); ok {
		reconstructed["id"] = id
	}
	if obj, ok := firstChunk["object"].(string); ok {
		// Change from "chat.completion.chunk" to "chat.completion"
		reconstructed["object"] = strings.Replace(obj, ".chunk", "", 1)
	}
	if created, ok := firstChunk["created"].(float64); ok {
		reconstructed["created"] = int64(created)
	}
	if model, ok := firstChunk["model"].(string); ok {
		reconstructed["model"] = model
	}
	if tier, ok := firstChunk["service_tier"].(string); ok {
		reconstructed["service_tier"] = tier
	}
	if fp, ok := firstChunk["system_fingerprint"].(string); ok {
		reconstructed["system_fingerprint"] = fp
	}

	// Reconstruct the message from deltas
	var contentBuilder strings.Builder
	var role string
	var toolCalls []interface{}
	var finishReason string
	var usage map[string]interface{}

	for _, chunk := range chunks {
		choices, ok := chunk.data["choices"].([]interface{})
		if !ok || len(choices) == 0 {
			continue
		}

		choice := choices[0].(map[string]interface{})
		delta, ok := choice["delta"].(map[string]interface{})
		if !ok {
			continue
		}

		// Collect role
		if r, ok := delta["role"].(string); ok && r != "" {
			role = r
		}

		// Collect content
		if content, ok := delta["content"].(string); ok {
			contentBuilder.WriteString(content)
		}

		// Collect tool calls
		if tc, ok := delta["tool_calls"].([]interface{}); ok {
			toolCalls = append(toolCalls, tc...)
		}

		// Collect finish reason
		if fr, ok := choice["finish_reason"].(string); ok && fr != "" {
			finishReason = fr
		}

		// Collect usage (usually in last chunk)
		if u, ok := chunk.data["usage"].(map[string]interface{}); ok {
			usage = u
		}
	}

	// Build choices array
	message := make(map[string]interface{})
	if role != "" {
		message["role"] = role
	}

	content := contentBuilder.String()
	if content != "" {
		message["content"] = content
	} else if len(toolCalls) > 0 {
		// For tool calls, content is null
		message["content"] = nil
		message["tool_calls"] = toolCalls
	}

	choice := map[string]interface{}{
		"index":   0,
		"message": message,
	}

	if finishReason != "" {
		choice["finish_reason"] = finishReason
	}

	reconstructed["choices"] = []interface{}{choice}

	// Add usage if available
	if usage != nil {
		reconstructed["usage"] = usage
	}

	// Convert to JSON
	jsonBytes, err := json.MarshalIndent(reconstructed, "", "  ")
	if err != nil {
		log.Printf("WARNING: Failed to marshal reconstructed response: %v", err)
		return "", nil
	}

	// Calculate streaming metadata
	metadata := &models.StreamingMetadata{
		ChunksReceived:          len(chunks),
		ReconstructedFromStream: true,
		FirstChunkTime:          0, // First chunk is immediate
		LastChunkTime:           time.Since(startTime),
	}

	return string(jsonBytes), metadata
}
