.PHONY: build run test clean docker-build docker-run

# Build the binary
build:
	go build -o aiblackbox ./cmd/proxy

# Run the application
run:
	go run ./cmd/proxy

# Run tests
test:
	go test -v ./...

# Clean build artifacts
clean:
	rm -f aiblackbox
	rm -rf logs/

# Build Docker image
docker-build:
	docker build -t aiblackbox/proxy:latest .

# Run Docker container
docker-run:
	docker run -p 8080:8080 \
		-v $(PWD)/config.yaml:/app/config.yaml \
		-v $(PWD)/logs:/app/logs \
		aiblackbox/proxy:latest

# Initialize go modules
init:
	go mod download
	go mod tidy

# Format code
fmt:
	go fmt ./...

# Run linter
lint:
	golangci-lint run

# Show help
help:
	@echo "Available targets:"
	@echo "  build        - Build the binary"
	@echo "  run          - Run the application"
	@echo "  test         - Run tests"
	@echo "  clean        - Clean build artifacts"
	@echo "  docker-build - Build Docker image"
	@echo "  docker-run   - Run Docker container"
	@echo "  init         - Initialize Go modules"
	@echo "  fmt          - Format code"
	@echo "  lint         - Run linter"
