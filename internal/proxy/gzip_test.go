package proxy

import (
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestDecompressGzipResponse tests that gzipped responses are decompressed in audit logs
func TestDecompressGzipResponse(t *testing.T) {
	responseJSON := `{"id":"test-123","choices":[{"message":{"content":"Hello, world!"}}]}`

	// Compress the response
	var compressed bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressed)
	gzipWriter.Write([]byte(responseJSON))
	gzipWriter.Close()

	// Create a response capturer and simulate receiving a gzipped response
	rec := httptest.NewRecorder()
	rec.Header().Set("Content-Type", "application/json")
	rec.Header().Set("Content-Encoding", "gzip")

	capturer := NewResponseCapturer(rec)
	capturer.WriteHeader(http.StatusOK)
	capturer.Write(compressed.Bytes())

	// Verify the raw body is still gzipped
	rawBody := capturer.Body()
	if len(rawBody) < 2 || rawBody[0] != '\x1f' || rawBody[1] != '\x8b' {
		t.Error("Raw body should still be gzipped")
	}

	// Verify DecompressedBody returns the decompressed content
	decompressed := capturer.DecompressedBody()
	if decompressed != responseJSON {
		t.Errorf("DecompressedBody failed\nExpected: %s\nGot: %s", responseJSON, decompressed)
	}

	t.Logf("✓ Gzip decompression working correctly")
	t.Logf("  Compressed: %d bytes", len(rawBody))
	t.Logf("  Decompressed: %d bytes", len(decompressed))
}

// TestDecompressedBody tests the DecompressedBody method directly
func TestDecompressedBody(t *testing.T) {
	tests := []struct {
		name              string
		body              string
		contentEncoding   string
		expectedOutput    string
		shouldDecompress  bool
	}{
		{
			name:             "Plain text response",
			body:             `{"message":"hello"}`,
			contentEncoding:  "",
			expectedOutput:   `{"message":"hello"}`,
			shouldDecompress: false,
		},
		{
			name:             "Non-gzipped with gzip header",
			body:             `{"message":"hello"}`,
			contentEncoding:  "gzip",
			expectedOutput:   `{"message":"hello"}`, // Should return as-is if not gzip format
			shouldDecompress: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			if tt.contentEncoding != "" {
				rec.Header().Set("Content-Encoding", tt.contentEncoding)
			}

			capturer := NewResponseCapturer(rec)
			capturer.headers.Set("Content-Encoding", tt.contentEncoding)
			capturer.body.WriteString(tt.body)

			result := capturer.DecompressedBody()

			if result != tt.expectedOutput {
				t.Errorf("Expected: %q, Got: %q", tt.expectedOutput, result)
			}
		})
	}
}

// TestDecompressGzipData tests actual gzip compression/decompression
func TestDecompressGzipData(t *testing.T) {
	originalData := `{"id":"chatcmpl-123","object":"chat.completion","choices":[{"message":{"role":"assistant","content":"The proxy is working correctly."}}]}`

	// Compress the data
	var compressed bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressed)
	_, err := gzipWriter.Write([]byte(originalData))
	if err != nil {
		t.Fatalf("Failed to compress data: %v", err)
	}
	gzipWriter.Close()

	// Create capturer with compressed data
	rec := httptest.NewRecorder()
	rec.Header().Set("Content-Encoding", "gzip")

	capturer := NewResponseCapturer(rec)
	capturer.headers.Set("Content-Encoding", "gzip")
	capturer.body.WriteString(compressed.String())

	// Decompress using our method
	decompressed := capturer.DecompressedBody()

	// Verify decompression
	if decompressed != originalData {
		t.Errorf("Decompression failed\nExpected: %s\nGot: %s", originalData, decompressed)
	}

	t.Logf("✓ Successfully decompressed gzip data")
	t.Logf("  Original: %d bytes", len(originalData))
	t.Logf("  Compressed: %d bytes", compressed.Len())
	t.Logf("  Decompressed: %d bytes", len(decompressed))
}
