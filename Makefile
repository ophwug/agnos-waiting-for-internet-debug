# Makefile for the AGNOS/openpilot setup connectivity debugger

BINARY_NAME_WINDOWS=agnos-waiting-for-internet-debug.exe
BINARY_NAME_MACOS_AMD64=agnos-waiting-for-internet-debug-darwin-amd64
BINARY_NAME_MACOS_ARM64=agnos-waiting-for-internet-debug-darwin-arm64
BINARY_NAME_LINUX=agnos-waiting-for-internet-debug-linux
CMD=./cmd/debugger
VERSION?=$(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT?=$(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE?=$(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS=-X 'main.version=$(VERSION)' -X 'main.commit=$(COMMIT)' -X 'main.date=$(DATE)'

# Default target executed when you run `make`
all: build-windows build-macos build-linux

# Build the Go application for Windows
build-windows:
	@echo "Building for Windows..."
	GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(BINARY_NAME_WINDOWS) $(CMD)

# Build the Go application for macOS (Intel and Apple Silicon)
build-macos: build-macos-amd64 build-macos-arm64

build-macos-amd64:
	@echo "Building for macOS (Intel)..."
	GOOS=darwin GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(BINARY_NAME_MACOS_AMD64) $(CMD)

build-macos-arm64:
	@echo "Building for macOS (Apple Silicon)..."
	GOOS=darwin GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(BINARY_NAME_MACOS_ARM64) $(CMD)

# Build the Go application for Linux
build-linux:
	@echo "Building for Linux..."
	GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(BINARY_NAME_LINUX) $(CMD)

# Clean up the build artifacts
clean:
	@echo "Cleaning up..."
	@rm -f $(BINARY_NAME_WINDOWS) $(BINARY_NAME_MACOS_AMD64) $(BINARY_NAME_MACOS_ARM64) $(BINARY_NAME_LINUX)

# Run the Go application for development
run:
	@echo "Running the application for development..."
	@go run $(CMD)

# A phony target to avoid conflicts with a file named 'clean'
.PHONY: all build-windows build-macos build-macos-amd64 build-macos-arm64 build-linux clean run
