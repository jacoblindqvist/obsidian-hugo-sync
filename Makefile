# Obsidian → Hugo Sync Daemon Makefile

# Build variables
BINARY_NAME=obsidian-hugo-sync
MAIN_PATH=./cmd/obsidian-hugo-sync
BUILD_DIR=build
VERSION?=dev
COMMIT?=$(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
LDFLAGS=-ldflags "-X main.version=$(VERSION) -X main.commit=$(COMMIT)"

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
GOMOD=$(GOCMD) mod

.PHONY: all build clean test test-verbose install deps help

# Default target
all: clean test build

# Build the binary
build:
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p $(BUILD_DIR)
	$(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) $(MAIN_PATH)
	@echo "Built $(BUILD_DIR)/$(BINARY_NAME)"

# Clean build artifacts
clean:
	@echo "Cleaning..."
	$(GOCLEAN)
	@rm -rf $(BUILD_DIR)
	@echo "Clean complete"

# Run tests
test:
	@echo "Running tests..."
	$(GOTEST) ./...

# Run tests with verbose output
test-verbose:
	@echo "Running tests (verbose)..."
	$(GOTEST) -v ./...

# Run tests with coverage
test-coverage:
	@echo "Running tests with coverage..."
	$(GOTEST) -coverprofile=coverage.out ./...
	$(GOCMD) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

# Install dependencies
deps:
	@echo "Installing dependencies..."
	$(GOMOD) download
	$(GOMOD) tidy

# Install binary to system PATH
install: build
	@echo "Installing $(BINARY_NAME) to /usr/local/bin/"
	@sudo cp $(BUILD_DIR)/$(BINARY_NAME) /usr/local/bin/
	@echo "Installed successfully"

# Uninstall binary from system PATH
uninstall:
	@echo "Uninstalling $(BINARY_NAME)..."
	@sudo rm -f /usr/local/bin/$(BINARY_NAME)
	@echo "Uninstalled successfully"

# Development build (no optimization)
dev:
	@echo "Building development version..."
	@mkdir -p $(BUILD_DIR)
	$(GOBUILD) -race -o $(BUILD_DIR)/$(BINARY_NAME)-dev $(MAIN_PATH)
	@echo "Built $(BUILD_DIR)/$(BINARY_NAME)-dev"

# Run with example configuration
run-example: build
	@echo "Running example (dry-run mode)..."
	./$(BUILD_DIR)/$(BINARY_NAME) \
		--vault ./example/vault \
		--repo ./example/hugo-site \
		--dry-run \
		--log-level debug

# Lint code
lint:
	@echo "Running linter..."
	@command -v golangci-lint >/dev/null 2>&1 || { \
		echo "golangci-lint not installed. Install with:"; \
		echo "go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"; \
		exit 1; \
	}
	golangci-lint run

# Format code
fmt:
	@echo "Formatting code..."
	$(GOCMD) fmt ./...

# Check for security issues
sec:
	@echo "Running security check..."
	@command -v gosec >/dev/null 2>&1 || { \
		echo "gosec not installed. Install with:"; \
		echo "go install github.com/securecodewarrior/gosec/v2/cmd/gosec@latest"; \
		exit 1; \
	}
	gosec ./...

# Run all checks (format, lint, security, test)
check: fmt lint sec test

# Release build for multiple platforms
release:
	@echo "Building release binaries..."
	@mkdir -p $(BUILD_DIR)/release
	
	# Linux AMD64
	GOOS=linux GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/release/$(BINARY_NAME)-linux-amd64 $(MAIN_PATH)
	
	# Linux ARM64
	GOOS=linux GOARCH=arm64 $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/release/$(BINARY_NAME)-linux-arm64 $(MAIN_PATH)
	
	# macOS AMD64
	GOOS=darwin GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/release/$(BINARY_NAME)-darwin-amd64 $(MAIN_PATH)
	
	# macOS ARM64 (Apple Silicon)
	GOOS=darwin GOARCH=arm64 $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/release/$(BINARY_NAME)-darwin-arm64 $(MAIN_PATH)
	
	# Windows AMD64
	GOOS=windows GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/release/$(BINARY_NAME)-windows-amd64.exe $(MAIN_PATH)
	
	@echo "Release binaries built in $(BUILD_DIR)/release/"

# Show help
help:
	@echo "Obsidian → Hugo Sync Daemon Build System"
	@echo ""
	@echo "Targets:"
	@echo "  build         Build the binary"
	@echo "  clean         Clean build artifacts"
	@echo "  test          Run tests"
	@echo "  test-verbose  Run tests with verbose output"
	@echo "  test-coverage Run tests with coverage report"
	@echo "  deps          Install dependencies"
	@echo "  install       Install binary to system PATH"
	@echo "  uninstall     Remove binary from system PATH"
	@echo "  dev           Build development version with race detection"
	@echo "  run-example   Run with example configuration"
	@echo "  lint          Run code linter"
	@echo "  fmt           Format code"
	@echo "  sec           Run security checks"
	@echo "  check         Run all checks (format, lint, security, test)"
	@echo "  release       Build release binaries for multiple platforms"
	@echo "  help          Show this help message"
	@echo ""
	@echo "Variables:"
	@echo "  VERSION       Version string (default: dev)"
	@echo "  COMMIT        Git commit hash (auto-detected)"
	@echo ""
	@echo "Examples:"
	@echo "  make build VERSION=1.0.0"
	@echo "  make test-verbose"
	@echo "  make install"
	@echo "  make release VERSION=1.0.0" 