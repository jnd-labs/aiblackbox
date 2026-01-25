package trace

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// ConversationMetadata extracts conversation threading information
type ConversationMetadata struct {
	MessageCount    int
	HasAssistant    bool
	HasToolMessages bool
	ConversationID  string // Hash of first user message for grouping
}

// ExtractConversationMetadata analyzes request body to extract conversation context
func ExtractConversationMetadata(requestBody string) *ConversationMetadata {
	if requestBody == "" {
		return nil
	}

	var req struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content,omitempty"`
		} `json:"messages"`
	}

	if err := json.Unmarshal([]byte(requestBody), &req); err != nil {
		return nil
	}

	if len(req.Messages) == 0 {
		return nil
	}

	metadata := &ConversationMetadata{
		MessageCount: len(req.Messages),
	}

	// Find first user message to generate conversation ID
	var firstUserContent string
	for _, msg := range req.Messages {
		switch msg.Role {
		case "assistant":
			metadata.HasAssistant = true
		case "tool":
			metadata.HasToolMessages = true
		case "user":
			if firstUserContent == "" && msg.Content != "" {
				firstUserContent = msg.Content
			}
		}
	}

	// Generate conversation ID from first user message
	if firstUserContent != "" {
		hash := sha256.Sum256([]byte(firstUserContent))
		metadata.ConversationID = hex.EncodeToString(hash[:8]) // First 16 hex chars
	}

	return metadata
}

// IsMultiTurnConversation determines if this is likely a multi-turn conversation
func IsMultiTurnConversation(requestBody string) bool {
	metadata := ExtractConversationMetadata(requestBody)
	if metadata == nil {
		return false
	}

	// Multi-turn if:
	// - Has assistant messages (previous responses)
	// - Has tool messages (tool call workflow)
	// - More than 2 messages (system + user is minimum)
	return metadata.HasAssistant || metadata.HasToolMessages || metadata.MessageCount > 2
}
