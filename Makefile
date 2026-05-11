# Makefile for the AGNOS/openpilot setup connectivity debugger

BINARY_NAME_WINDOWS=agnos-waiting-for-internet-debug.exe
BINARY_NAME_MACOS=agnos-waiting-for-internet-debug-darwin
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

# Build the Go application for macOS
build-macos:
	@echo "Building for macOS..."
	GOOS=darwin GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(BINARY_NAME_MACOS) $(CMD)

# Build the Go application for Linux
build-linux:
	@echo "Building for Linux..."
	GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(BINARY_NAME_LINUX) $(CMD)

# Clean up the build artifacts
clean:
	@echo "Cleaning up..."
	@rm -f $(BINARY_NAME_WINDOWS) $(BINARY_NAME_MACOS) $(BINARY_NAME_LINUX)

# Run the Go application for development
run:
	@echo "Running the application for development..."
	@go run $(CMD)

# A phony target to avoid conflicts with a file named 'clean'
.PHONY: all build clean run
