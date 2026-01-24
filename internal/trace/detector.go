package trace

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"

	"github.com/jnd-labs/aiblackbox/internal/models"
)

// OpenAI API response structure for tool calls
type openAIResponse struct {
	Choices []struct {
		Message struct {
			ToolCalls []struct {
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
}

// OpenAI API request structure for tool results
type openAIRequest struct {
	Messages []struct {
		Role       string `json:"role"`
		ToolCallID string `json:"tool_call_id,omitempty"`
		Content    string `json:"content,omitempty"`
	} `json:"messages"`
}

// DetectToolCalls extracts OpenAI tool call information from a response body
// Returns the first tool call found, or nil if none present
func DetectToolCalls(responseBody string) *models.ToolCallInfo {
	if responseBody == "" {
		return nil
	}

	var resp openAIResponse
	if err := json.Unmarshal([]byte(responseBody), &resp); err != nil {
		// Not valid JSON or not in expected format - this is normal for non-tool-call responses
		return nil
	}

	// Check if there are any choices with tool calls
	if len(resp.Choices) == 0 {
		return nil
	}

	toolCalls := resp.Choices[0].Message.ToolCalls
	if len(toolCalls) == 0 {
		return nil
	}

	// Extract the first tool call
	tc := toolCalls[0]

	// Compute SHA256 hash of arguments for integrity
	argsHash := sha256.Sum256([]byte(tc.Function.Arguments))
	argsHashStr := hex.EncodeToString(argsHash[:])

	return &models.ToolCallInfo{
		ID:   tc.ID,
		Type: tc.Type,
		Function: models.FunctionCall{
			Name:          tc.Function.Name,
			Arguments:     tc.Function.Arguments,
			ArgumentsHash: argsHashStr,
		},
		Index: 0, // For now, we only track the first tool call
	}
}

// DetectToolResults extracts OpenAI tool result information from a request body
// Returns the first tool result found, or nil if none present
func DetectToolResults(requestBody string) *models.ToolResultInfo {
	if requestBody == "" {
		return nil
	}

	var req openAIRequest
	if err := json.Unmarshal([]byte(requestBody), &req); err != nil {
		// Not valid JSON or not in expected format - this is normal
		return nil
	}

	// Look for the first message with role "tool"
	for _, msg := range req.Messages {
		if msg.Role == "tool" && msg.ToolCallID != "" {
			// Compute SHA256 hash of content for integrity
			contentHash := sha256.Sum256([]byte(msg.Content))
			contentHashStr := hex.EncodeToString(contentHash[:])

			// Check if content indicates an error
			isError := false
			errorMessage := ""

			// Try to parse content as JSON to check for error field
			var contentObj map[string]interface{}
			if err := json.Unmarshal([]byte(msg.Content), &contentObj); err == nil {
				if errField, exists := contentObj["error"]; exists {
					isError = true
					if errStr, ok := errField.(string); ok {
						errorMessage = errStr
					} else {
						// Error field exists but not a string, convert to JSON
						if errBytes, err := json.Marshal(errField); err == nil {
							errorMessage = string(errBytes)
						}
					}
				}
			}

			return &models.ToolResultInfo{
				ToolCallID:   msg.ToolCallID,
				Content:      msg.Content,
				ContentHash:  contentHashStr,
				IsError:      isError,
				ErrorMessage: errorMessage,
			}
		}
	}

	return nil
}

// DetermineSpanType determines the span type based on request and response content
func DetermineSpanType(requestBody, responseBody string) models.SpanType {
	// Check if response contains tool calls
	if DetectToolCalls(responseBody) != nil {
		return models.SpanTypeToolCall
	}

	// Check if request contains tool results
	if DetectToolResults(requestBody) != nil {
		return models.SpanTypeToolResult
	}

	// Check if this is likely a final response (no tool calls in response)
	var resp openAIResponse
	if err := json.Unmarshal([]byte(responseBody), &resp); err == nil {
		if len(resp.Choices) > 0 {
			// Response has choices but no tool calls - likely final response
			return models.SpanTypeFinalResponse
		}
	}

	// Default to agent thinking for OpenAI chat completions
	return models.SpanTypeAgentThinking
}

// GenerateSpanName creates a human-readable span name based on the span type and content
func GenerateSpanName(spanType models.SpanType, toolCall *models.ToolCallInfo, toolResult *models.ToolResultInfo) string {
	switch spanType {
	case models.SpanTypeUserPrompt:
		return "user_prompt"
	case models.SpanTypeAgentThinking:
		return "agent_thinking"
	case models.SpanTypeToolCall:
		if toolCall != nil {
			return toolCall.Function.Name
		}
		return "tool_call"
	case models.SpanTypeToolResult:
		if toolResult != nil {
			if toolResult.IsError {
				return "tool_error"
			}
			return "tool_result"
		}
		return "tool_result"
	case models.SpanTypeFinalResponse:
		return "final_response"
	case models.SpanTypeError:
		return "error"
	default:
		return "unknown"
	}
}

// EnrichTraceContext enriches a trace context with tool call/result information
// This is called after the response is received to populate tool-related fields
func EnrichTraceContext(trace *models.TraceContext, requestBody, responseBody string) {
	if trace == nil {
		return
	}

	// Detect tool calls in response
	toolCall := DetectToolCalls(responseBody)
	if toolCall != nil {
		trace.ToolCall = toolCall
		trace.SpanType = models.SpanTypeToolCall
		trace.SpanName = GenerateSpanName(models.SpanTypeToolCall, toolCall, nil)
		log.Printf("INFO: Detected tool call: trace=%s, span=%s, tool=%s, call_id=%s",
			trace.TraceID, trace.SpanID, toolCall.Function.Name, toolCall.ID)
		return
	}

	// Detect tool results in request
	toolResult := DetectToolResults(requestBody)
	if toolResult != nil {
		trace.ToolResult = toolResult
		trace.SpanType = models.SpanTypeToolResult
		trace.SpanName = GenerateSpanName(models.SpanTypeToolResult, nil, toolResult)
		log.Printf("INFO: Detected tool result: trace=%s, span=%s, call_id=%s, is_error=%v",
			trace.TraceID, trace.SpanID, toolResult.ToolCallID, toolResult.IsError)
		return
	}

	// Determine span type based on content
	spanType := DetermineSpanType(requestBody, responseBody)
	trace.SpanType = spanType
	trace.SpanName = GenerateSpanName(spanType, nil, nil)
}
