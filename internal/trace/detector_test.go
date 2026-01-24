package trace

import (
	"testing"

	"github.com/jnd-labs/aiblackbox/internal/models"
)

// TestDetectToolCalls_ValidResponse verifies tool call detection from OpenAI response
func TestDetectToolCalls_ValidResponse(t *testing.T) {
	responseBody := `{
		"id": "chatcmpl-123",
		"choices": [{
			"message": {
				"role": "assistant",
				"tool_calls": [{
					"id": "call_abc123",
					"type": "function",
					"function": {
						"name": "get_weather",
						"arguments": "{\"city\": \"London\", \"units\": \"celsius\"}"
					}
				}]
			}
		}]
	}`

	toolCall := DetectToolCalls(responseBody)

	if toolCall == nil {
		t.Fatal("Expected tool call to be detected, got nil")
	}

	if toolCall.ID != "call_abc123" {
		t.Errorf("Expected ID 'call_abc123', got '%s'", toolCall.ID)
	}

	if toolCall.Type != "function" {
		t.Errorf("Expected type 'function', got '%s'", toolCall.Type)
	}

	if toolCall.Function.Name != "get_weather" {
		t.Errorf("Expected function name 'get_weather', got '%s'", toolCall.Function.Name)
	}

	expectedArgs := `{"city": "London", "units": "celsius"}`
	if toolCall.Function.Arguments != expectedArgs {
		t.Errorf("Expected arguments '%s', got '%s'", expectedArgs, toolCall.Function.Arguments)
	}

	// Verify hash is computed
	if toolCall.Function.ArgumentsHash == "" {
		t.Error("ArgumentsHash should not be empty")
	}

	if len(toolCall.Function.ArgumentsHash) != 64 {
		t.Errorf("Expected hash length 64, got %d", len(toolCall.Function.ArgumentsHash))
	}
}

// TestDetectToolCalls_NoToolCalls verifies nil is returned when no tool calls present
func TestDetectToolCalls_NoToolCalls(t *testing.T) {
	responseBody := `{
		"id": "chatcmpl-123",
		"choices": [{
			"message": {
				"role": "assistant",
				"content": "The weather in London is 15°C."
			}
		}]
	}`

	toolCall := DetectToolCalls(responseBody)

	if toolCall != nil {
		t.Errorf("Expected nil, got %+v", toolCall)
	}
}

// TestDetectToolCalls_EmptyBody verifies nil is returned for empty body
func TestDetectToolCalls_EmptyBody(t *testing.T) {
	toolCall := DetectToolCalls("")

	if toolCall != nil {
		t.Errorf("Expected nil for empty body, got %+v", toolCall)
	}
}

// TestDetectToolCalls_InvalidJSON verifies nil is returned for invalid JSON
func TestDetectToolCalls_InvalidJSON(t *testing.T) {
	responseBody := `{invalid json`

	toolCall := DetectToolCalls(responseBody)

	if toolCall != nil {
		t.Errorf("Expected nil for invalid JSON, got %+v", toolCall)
	}
}

// TestDetectToolCalls_MultipleToolCalls verifies first tool call is returned
func TestDetectToolCalls_MultipleToolCalls(t *testing.T) {
	responseBody := `{
		"choices": [{
			"message": {
				"tool_calls": [
					{
						"id": "call_first",
						"type": "function",
						"function": {"name": "first_tool", "arguments": "{}"}
					},
					{
						"id": "call_second",
						"type": "function",
						"function": {"name": "second_tool", "arguments": "{}"}
					}
				]
			}
		}]
	}`

	toolCall := DetectToolCalls(responseBody)

	if toolCall == nil {
		t.Fatal("Expected tool call to be detected")
	}

	if toolCall.ID != "call_first" {
		t.Errorf("Expected first tool call ID 'call_first', got '%s'", toolCall.ID)
	}

	if toolCall.Function.Name != "first_tool" {
		t.Errorf("Expected first tool name 'first_tool', got '%s'", toolCall.Function.Name)
	}
}

// TestDetectToolResults_ValidRequest verifies tool result detection from OpenAI request
func TestDetectToolResults_ValidRequest(t *testing.T) {
	requestBody := `{
		"model": "gpt-4",
		"messages": [
			{
				"role": "user",
				"content": "What's the weather in London?"
			},
			{
				"role": "assistant",
				"tool_calls": [{"id": "call_abc123", "type": "function", "function": {"name": "get_weather", "arguments": "{\"city\": \"London\"}"}}]
			},
			{
				"role": "tool",
				"tool_call_id": "call_abc123",
				"content": "{\"temperature\": 15, \"conditions\": \"cloudy\"}"
			}
		]
	}`

	toolResult := DetectToolResults(requestBody)

	if toolResult == nil {
		t.Fatal("Expected tool result to be detected, got nil")
	}

	if toolResult.ToolCallID != "call_abc123" {
		t.Errorf("Expected ToolCallID 'call_abc123', got '%s'", toolResult.ToolCallID)
	}

	expectedContent := `{"temperature": 15, "conditions": "cloudy"}`
	if toolResult.Content != expectedContent {
		t.Errorf("Expected content '%s', got '%s'", expectedContent, toolResult.Content)
	}

	// Verify hash is computed
	if toolResult.ContentHash == "" {
		t.Error("ContentHash should not be empty")
	}

	if len(toolResult.ContentHash) != 64 {
		t.Errorf("Expected hash length 64, got %d", len(toolResult.ContentHash))
	}

	if toolResult.IsError {
		t.Error("Expected IsError to be false")
	}
}

// TestDetectToolResults_ErrorResult verifies error detection in tool result
func TestDetectToolResults_ErrorResult(t *testing.T) {
	requestBody := `{
		"messages": [
			{
				"role": "tool",
				"tool_call_id": "call_error123",
				"content": "{\"error\": \"API key is invalid\"}"
			}
		]
	}`

	toolResult := DetectToolResults(requestBody)

	if toolResult == nil {
		t.Fatal("Expected tool result to be detected")
	}

	if !toolResult.IsError {
		t.Error("Expected IsError to be true")
	}

	if toolResult.ErrorMessage != "API key is invalid" {
		t.Errorf("Expected error message 'API key is invalid', got '%s'", toolResult.ErrorMessage)
	}
}

// TestDetectToolResults_NoToolResults verifies nil is returned when no tool results present
func TestDetectToolResults_NoToolResults(t *testing.T) {
	requestBody := `{
		"model": "gpt-4",
		"messages": [
			{
				"role": "user",
				"content": "What's the weather in London?"
			}
		]
	}`

	toolResult := DetectToolResults(requestBody)

	if toolResult != nil {
		t.Errorf("Expected nil, got %+v", toolResult)
	}
}

// TestDetectToolResults_EmptyBody verifies nil is returned for empty body
func TestDetectToolResults_EmptyBody(t *testing.T) {
	toolResult := DetectToolResults("")

	if toolResult != nil {
		t.Errorf("Expected nil for empty body, got %+v", toolResult)
	}
}

// TestDetermineSpanType_ToolCall verifies tool call span type detection
func TestDetermineSpanType_ToolCall(t *testing.T) {
	requestBody := `{"messages": [{"role": "user", "content": "test"}]}`
	responseBody := `{
		"choices": [{
			"message": {
				"tool_calls": [{
					"id": "call_123",
					"type": "function",
					"function": {"name": "test_tool", "arguments": "{}"}
				}]
			}
		}]
	}`

	spanType := DetermineSpanType(requestBody, responseBody)

	if spanType != models.SpanTypeToolCall {
		t.Errorf("Expected SpanTypeToolCall, got %s", spanType)
	}
}

// TestDetermineSpanType_ToolResult verifies tool result span type detection
func TestDetermineSpanType_ToolResult(t *testing.T) {
	requestBody := `{
		"messages": [{
			"role": "tool",
			"tool_call_id": "call_123",
			"content": "{\"result\": \"success\"}"
		}]
	}`
	responseBody := `{"choices": [{"message": {"content": "test"}}]}`

	spanType := DetermineSpanType(requestBody, responseBody)

	if spanType != models.SpanTypeToolResult {
		t.Errorf("Expected SpanTypeToolResult, got %s", spanType)
	}
}

// TestDetermineSpanType_FinalResponse verifies final response span type detection
func TestDetermineSpanType_FinalResponse(t *testing.T) {
	requestBody := `{"messages": [{"role": "user", "content": "test"}]}`
	responseBody := `{"choices": [{"message": {"content": "Final answer"}}]}`

	spanType := DetermineSpanType(requestBody, responseBody)

	if spanType != models.SpanTypeFinalResponse {
		t.Errorf("Expected SpanTypeFinalResponse, got %s", spanType)
	}
}

// TestDetermineSpanType_AgentThinking verifies agent thinking as default
func TestDetermineSpanType_AgentThinking(t *testing.T) {
	requestBody := `{"messages": []}`
	responseBody := `{"invalid": "format"}`

	spanType := DetermineSpanType(requestBody, responseBody)

	if spanType != models.SpanTypeAgentThinking {
		t.Errorf("Expected SpanTypeAgentThinking, got %s", spanType)
	}
}

// TestGenerateSpanName verifies span name generation
func TestGenerateSpanName(t *testing.T) {
	tests := []struct {
		name     string
		spanType models.SpanType
		toolCall *models.ToolCallInfo
		toolResult *models.ToolResultInfo
		expected string
	}{
		{
			name:     "user prompt",
			spanType: models.SpanTypeUserPrompt,
			expected: "user_prompt",
		},
		{
			name:     "agent thinking",
			spanType: models.SpanTypeAgentThinking,
			expected: "agent_thinking",
		},
		{
			name:     "tool call with name",
			spanType: models.SpanTypeToolCall,
			toolCall: &models.ToolCallInfo{
				Function: models.FunctionCall{Name: "get_weather"},
			},
			expected: "get_weather",
		},
		{
			name:     "tool call without name",
			spanType: models.SpanTypeToolCall,
			expected: "tool_call",
		},
		{
			name:     "tool result success",
			spanType: models.SpanTypeToolResult,
			toolResult: &models.ToolResultInfo{IsError: false},
			expected: "tool_result",
		},
		{
			name:     "tool result error",
			spanType: models.SpanTypeToolResult,
			toolResult: &models.ToolResultInfo{IsError: true},
			expected: "tool_error",
		},
		{
			name:     "final response",
			spanType: models.SpanTypeFinalResponse,
			expected: "final_response",
		},
		{
			name:     "error",
			spanType: models.SpanTypeError,
			expected: "error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GenerateSpanName(tt.spanType, tt.toolCall, tt.toolResult)
			if result != tt.expected {
				t.Errorf("Expected '%s', got '%s'", tt.expected, result)
			}
		})
	}
}

// TestEnrichTraceContext_ToolCall verifies trace enrichment with tool call
func TestEnrichTraceContext_ToolCall(t *testing.T) {
	trace := &models.TraceContext{
		TraceID: "trace123",
		SpanID:  "span456",
	}

	requestBody := `{"messages": []}`
	responseBody := `{
		"choices": [{
			"message": {
				"tool_calls": [{
					"id": "call_test",
					"type": "function",
					"function": {"name": "test_tool", "arguments": "{}"}
				}]
			}
		}]
	}`

	EnrichTraceContext(trace, requestBody, responseBody)

	if trace.SpanType != models.SpanTypeToolCall {
		t.Errorf("Expected SpanType to be ToolCall, got %s", trace.SpanType)
	}

	if trace.SpanName != "test_tool" {
		t.Errorf("Expected SpanName to be 'test_tool', got '%s'", trace.SpanName)
	}

	if trace.ToolCall == nil {
		t.Fatal("Expected ToolCall to be populated")
	}

	if trace.ToolCall.ID != "call_test" {
		t.Errorf("Expected ToolCall ID 'call_test', got '%s'", trace.ToolCall.ID)
	}
}

// TestEnrichTraceContext_ToolResult verifies trace enrichment with tool result
func TestEnrichTraceContext_ToolResult(t *testing.T) {
	trace := &models.TraceContext{
		TraceID: "trace123",
		SpanID:  "span456",
	}

	requestBody := `{
		"messages": [{
			"role": "tool",
			"tool_call_id": "call_result",
			"content": "{\"data\": \"test\"}"
		}]
	}`
	responseBody := `{"choices": [{"message": {"content": "response"}}]}`

	EnrichTraceContext(trace, requestBody, responseBody)

	if trace.SpanType != models.SpanTypeToolResult {
		t.Errorf("Expected SpanType to be ToolResult, got %s", trace.SpanType)
	}

	if trace.SpanName != "tool_result" {
		t.Errorf("Expected SpanName to be 'tool_result', got '%s'", trace.SpanName)
	}

	if trace.ToolResult == nil {
		t.Fatal("Expected ToolResult to be populated")
	}

	if trace.ToolResult.ToolCallID != "call_result" {
		t.Errorf("Expected ToolResult ToolCallID 'call_result', got '%s'", trace.ToolResult.ToolCallID)
	}
}

// TestEnrichTraceContext_NilTrace verifies nil trace is handled gracefully
func TestEnrichTraceContext_NilTrace(t *testing.T) {
	// Should not panic
	EnrichTraceContext(nil, `{}`, `{}`)
}

// TestEnrichTraceContext_FinalResponse verifies final response enrichment
func TestEnrichTraceContext_FinalResponse(t *testing.T) {
	trace := &models.TraceContext{
		TraceID: "trace123",
		SpanID:  "span456",
	}

	requestBody := `{"messages": [{"role": "user", "content": "test"}]}`
	responseBody := `{"choices": [{"message": {"content": "Final answer"}}]}`

	EnrichTraceContext(trace, requestBody, responseBody)

	if trace.SpanType != models.SpanTypeFinalResponse {
		t.Errorf("Expected SpanType to be FinalResponse, got %s", trace.SpanType)
	}

	if trace.SpanName != "final_response" {
		t.Errorf("Expected SpanName to be 'final_response', got '%s'", trace.SpanName)
	}

	if trace.ToolCall != nil {
		t.Error("Expected ToolCall to be nil for final response")
	}

	if trace.ToolResult != nil {
		t.Error("Expected ToolResult to be nil for final response")
	}
}
