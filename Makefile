.PHONY: build test lint integration-test editor-test clean all

# Default target
all: build test

# Build the binary
build:
	go build -o hass2ch ./cmd/hass2ch

# Run unit tests
test:
	go test -v ./...

# Run linter
lint:
	golangci-lint run

# Clean build artifacts
clean:
	rm -f hass2ch
	
.DEFAULT_GOAL := build