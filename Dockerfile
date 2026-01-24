# Build stage
FROM golang:1.21-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git ca-certificates

# Set working directory
WORKDIR /build

# Copy go mod files
COPY go.mod go.sum* ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the application
# CGO_ENABLED=0 for a static binary
# -ldflags="-w -s" to strip debug info and reduce size
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-w -s" \
    -o /build/aiblackbox \
    ./cmd/proxy

# Runtime stage
FROM alpine:latest

# Install CA certificates for HTTPS
RUN apk --no-cache add ca-certificates

# Create non-root user
RUN addgroup -g 1000 aiblackbox && \
    adduser -D -u 1000 -G aiblackbox aiblackbox

# Set working directory
WORKDIR /app

# Copy binary from builder
COPY --from=builder /build/aiblackbox /app/aiblackbox

# Create logs directory
RUN mkdir -p /app/logs && chown -R aiblackbox:aiblackbox /app

# Switch to non-root user
USER aiblackbox

# Expose proxy port
EXPOSE 8080

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8080/ || exit 1

# Run the application
ENTRYPOINT ["/app/aiblackbox"]
