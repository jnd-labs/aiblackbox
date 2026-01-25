package proxy

import (
	"net/http"
	"testing"
)

func TestSanitizeHeaders(t *testing.T) {
	h := &Handler{}

	tests := []struct {
		name     string
		input    map[string][]string
		expected map[string][]string
	}{
		{
			name: "Bearer token gets masked",
			input: map[string][]string{
				"Authorization": {"Bearer sk-proj-1234567890abcdefghijklmnop"},
				"Content-Type":  {"application/json"},
			},
			expected: map[string][]string{
				"Authorization": {"Bearer sk-...mnop"},
				"Content-Type":  {"application/json"},
			},
		},
		{
			name: "Multiple sensitive headers",
			input: map[string][]string{
				"Authorization": {"Bearer sk-test-1234567890"},
				"Cookie":        {"session=abc123def456ghi789"},
				"X-Api-Key":     {"secret-key-12345"},
				"User-Agent":    {"MyApp/1.0"},
			},
			expected: map[string][]string{
				"Authorization": {"Bearer sk-...7890"},
				"Cookie":        {"ses...i789"},
				"X-Api-Key":     {"sec...2345"},
				"User-Agent":    {"MyApp/1.0"},
			},
		},
		{
			name: "Short bearer token",
			input: map[string][]string{
				"Authorization": {"Bearer abc"},
			},
			expected: map[string][]string{
				"Authorization": {"Bearer [REDACTED]"},
			},
		},
		{
			name: "Empty authorization header",
			input: map[string][]string{
				"Authorization": {""},
			},
			expected: map[string][]string{
				"Authorization": {"[EMPTY]"},
			},
		},
		{
			name: "Case insensitive header matching",
			input: map[string][]string{
				"AUTHORIZATION": {"Bearer sk-test-1234567890"},
				"cookie":        {"session=abc123"},
				"X-API-KEY":     {"secret123"},
			},
			expected: map[string][]string{
				"AUTHORIZATION": {"Bearer sk-...7890"},
				"cookie":        {"ses...c123"},
				"X-API-KEY":     {"sec...t123"},
			},
		},
		{
			name: "Non-sensitive headers unchanged",
			input: map[string][]string{
				"Content-Type":   {"application/json"},
				"User-Agent":     {"MyApp/1.0"},
				"Accept":         {"*/*"},
				"Content-Length": {"1234"},
			},
			expected: map[string][]string{
				"Content-Type":   {"application/json"},
				"User-Agent":     {"MyApp/1.0"},
				"Accept":         {"*/*"},
				"Content-Length": {"1234"},
			},
		},
		{
			name: "Multiple values in header",
			input: map[string][]string{
				"Cookie": {"session=abc123def456", "token=xyz789uvw012"},
			},
			expected: map[string][]string{
				"Cookie": {"ses...f456", "tok...w012"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := h.sanitizeHeaders(tt.input)

			// Check all expected headers are present
			for key, expectedVals := range tt.expected {
				resultVals, ok := result[key]
				if !ok {
					t.Errorf("Expected header %q not found in result", key)
					continue
				}

				if len(resultVals) != len(expectedVals) {
					t.Errorf("Header %q: expected %d values, got %d", key, len(expectedVals), len(resultVals))
					continue
				}

				for i, expectedVal := range expectedVals {
					if resultVals[i] != expectedVal {
						t.Errorf("Header %q[%d]: expected %q, got %q", key, i, expectedVal, resultVals[i])
					}
				}
			}

			// Check no extra headers in result
			if len(result) != len(tt.expected) {
				t.Errorf("Expected %d headers, got %d", len(tt.expected), len(result))
			}
		})
	}
}

func TestMaskSensitiveValue(t *testing.T) {
	h := &Handler{}

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Bearer token - standard length",
			input:    "Bearer sk-proj-1234567890abcdefghijklmnop",
			expected: "Bearer sk-...mnop",
		},
		{
			name:     "Bearer token - lowercase",
			input:    "bearer sk-test-1234567890",
			expected: "Bearer sk-...7890",
		},
		{
			name:     "Bearer token - short",
			input:    "Bearer abc",
			expected: "Bearer [REDACTED]",
		},
		{
			name:     "Regular secret - long",
			input:    "secret-key-1234567890",
			expected: "sec...7890",
		},
		{
			name:     "Regular secret - short",
			input:    "secret",
			expected: "[REDACTED]",
		},
		{
			name:     "Empty value",
			input:    "",
			expected: "[EMPTY]",
		},
		{
			name:     "Session cookie",
			input:    "session=abc123def456ghi789",
			expected: "ses...i789",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := h.maskSensitiveValue(tt.input)
			if result != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestCloneHeadersDoesNotMutate(t *testing.T) {
	h := &Handler{}

	original := http.Header{
		"Authorization": []string{"Bearer sk-test-123"},
		"Content-Type":  []string{"application/json"},
	}

	cloned := h.cloneHeaders(original)
	sanitized := h.sanitizeHeaders(cloned)

	// Verify original is unchanged
	if original.Get("Authorization") != "Bearer sk-test-123" {
		t.Errorf("Original headers were mutated")
	}

	// Verify cloned is unchanged
	if cloned["Authorization"][0] != "Bearer sk-test-123" {
		t.Errorf("Cloned headers were mutated")
	}

	// Verify sanitized has masked value
	if sanitized["Authorization"][0] == "Bearer sk-test-123" {
		t.Errorf("Sanitized headers should have masked value")
	}
}
