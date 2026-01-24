#!/bin/bash

# Generate a large Base64 image (>100KB)
# Create 150KB of data (50,000 x "ABCD" = 200KB characters)
LARGE_IMAGE=$(python3 -c "print('ABCD' * 50000)")

# Create request body with large Base64 PNG
REQUEST_BODY=$(cat <<EOF
{
  "model": "gpt-4",
  "messages": [{
    "role": "user",
    "content": [{
      "type": "image_url",
      "image_url": {
        "url": "data:image/png;base64,${LARGE_IMAGE}"
      }
    }]
  }]
}
EOF
)

echo "Sending request with large Base64 image (>100KB)..."
echo "Expected behavior:"
echo "  1. Image extracted to logs/media/{date}/seq_N_request_0.png"
echo "  2. Audit log body contains: [IMAGE_EXTRACTED:0]"
echo "  3. Audit log includes media_references with file path and hash"
echo ""

# Make request to proxy (assuming it's running on port 8080)
curl -X POST http://localhost:8080/openai/chat/completions \
  -H "Content-Type: application/json" \
  -d "$REQUEST_BODY" \
  2>&1 | head -20

echo ""
echo "Check logs/audit.jsonl for media_references field"
echo "Check logs/media/ directory for extracted image files"
