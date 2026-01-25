package trace

import (
	"testing"
)

func TestExtractConversationMetadata(t *testing.T) {
	tests := []struct {
		name                 string
		requestBody          string
		expectedMessageCount int
		expectedHasAssistant bool
		expectedHasTools     bool
		expectConvID         bool
	}{
		{
			name: "Simple user prompt",
			requestBody: `{
				"messages": [
					{"role": "system", "content": "You are helpful"},
					{"role": "user", "content": "Hello"}
				]
			}`,
			expectedMessageCount: 2,
			expectedHasAssistant: false,
			expectedHasTools:     false,
			expectConvID:         true,
		},
		{
			name: "Multi-turn conversation",
			requestBody: `{
				"messages": [
					{"role": "system", "content": "You are helpful"},
					{"role": "user", "content": "What's 2+2?"},
					{"role": "assistant", "content": "4"},
					{"role": "user", "content": "And 4+4?"}
				]
			}`,
			expectedMessageCount: 4,
			expectedHasAssistant: true,
			expectedHasTools:     false,
			expectConvID:         true,
		},
		{
			name: "Tool call workflow",
			requestBody: `{
				"messages": [
					{"role": "user", "content": "Get weather"},
					{"role": "assistant", "tool_calls": [{"id": "call_123"}]},
					{"role": "tool", "tool_call_id": "call_123", "content": "Sunny"}
				]
			}`,
			expectedMessageCount: 3,
			expectedHasAssistant: true,
			expectedHasTools:     true,
			expectConvID:         true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metadata := ExtractConversationMetadata(tt.requestBody)

			if metadata == nil {
				t.Fatal("Expected metadata, got nil")
			}

			if metadata.MessageCount != tt.expectedMessageCount {
				t.Errorf("Message count: expected %d, got %d",
					tt.expectedMessageCount, metadata.MessageCount)
			}

			if metadata.HasAssistant != tt.expectedHasAssistant {
				t.Errorf("HasAssistant: expected %v, got %v",
					tt.expectedHasAssistant, metadata.HasAssistant)
			}

			if metadata.HasToolMessages != tt.expectedHasTools {
				t.Errorf("HasToolMessages: expected %v, got %v",
					tt.expectedHasTools, metadata.HasToolMessages)
			}

			if tt.expectConvID && metadata.ConversationID == "" {
				t.Error("Expected conversation ID, got empty string")
			}
		})
	}
}

func TestIsMultiTurnConversation(t *testing.T) {
	tests := []struct {
		name        string
		requestBody string
		expected    bool
	}{
		{
			name: "Single turn",
			requestBody: `{
				"messages": [
					{"role": "system", "content": "You are helpful"},
					{"role": "user", "content": "Hello"}
				]
			}`,
			expected: false,
		},
		{
			name: "Multi-turn with assistant",
			requestBody: `{
				"messages": [
					{"role": "user", "content": "Hi"},
					{"role": "assistant", "content": "Hello"},
					{"role": "user", "content": "How are you?"}
				]
			}`,
			expected: true,
		},
		{
			name: "Tool workflow",
			requestBody: `{
				"messages": [
					{"role": "user", "content": "Get weather"},
					{"role": "tool", "tool_call_id": "call_123", "content": "Sunny"}
				]
			}`,
			expected: true,
		},
		{
			name: "Long single turn",
			requestBody: `{
				"messages": [
					{"role": "system", "content": "You are helpful"},
					{"role": "user", "content": "Question 1"},
					{"role": "user", "content": "Question 2"}
				]
			}`,
			expected: true, // More than 2 messages
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsMultiTurnConversation(tt.requestBody)
			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestConversationIDConsistency(t *testing.T) {
	// Same first user message should produce same conversation ID
	request1 := `{"messages": [{"role": "user", "content": "Hello"}]}`
	request2 := `{"messages": [
		{"role": "user", "content": "Hello"},
		{"role": "assistant", "content": "Hi there"}
	]}`

	meta1 := ExtractConversationMetadata(request1)
	meta2 := ExtractConversationMetadata(request2)

	if meta1.ConversationID != meta2.ConversationID {
		t.Errorf("Expected same conversation ID for same first user message\nGot: %s vs %s",
			meta1.ConversationID, meta2.ConversationID)
	}

	t.Logf("✓ Conversation ID consistent: %s", meta1.ConversationID)
}
