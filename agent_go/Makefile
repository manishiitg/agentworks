.PHONY: help lint lint-fix install-linter test build clean mod-tidy

# Default target
help:
	@echo "Available targets:"
	@echo "  make lint        - Run golangci-lint"
	@echo "  make lint-fix     - Run golangci-lint with auto-fix"
	@echo "  make install-linter - Install golangci-lint"
	@echo "  make test        - Run Go tests"
	@echo "  make build       - Build the project"
	@echo "  make clean       - Clean build artifacts"
	@echo "  make mod-tidy    - Run go mod tidy (excludes tool_output_folder)"

# Install golangci-lint
install-linter:
	@echo "Installing golangci-lint..."
	@which golangci-lint > /dev/null || \
		curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | \
		sh -s -- -b $$(go env GOPATH)/bin latest
	@golangci-lint version
	@echo "✓ golangci-lint installed successfully"

# Run linter
lint: install-linter
	@echo "Running golangci-lint..."
	@golangci-lint run ./...
	@python3 ../scripts/check-unused.py go

# Run linter with auto-fix
lint-fix: install-linter
	@echo "Running golangci-lint with auto-fix..."
	@golangci-lint run --fix ./...

# Run tests
test:
	@echo "Running tests..."
	@go test ./... -v

# Build the project
build:
	@echo "Building project..."
	@go build -o bin/orchestrator .

# Clean build artifacts
clean:
	@echo "Cleaning build artifacts..."
	@rm -rf bin/
	@go clean ./...

# Run go mod tidy (excludes tool_output_folder)
mod-tidy:
	@echo "Running go mod tidy (excluding tool_output_folder)..."
	@if [ -f go-mod-tidy.sh ]; then \
		./go-mod-tidy.sh; \
	else \
		echo "⚠️  go-mod-tidy.sh not found, running go mod tidy directly..."; \
		go mod tidy; \
	fi

