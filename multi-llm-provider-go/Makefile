.PHONY: help lint lint-fix install-linter test build build-chat build-mcp build-all clean install-hooks scan-secrets install-act test-ci test-ci-job

# Default target
help:
	@echo "Available targets:"
	@echo "  make lint        - Run golangci-lint"
	@echo "  make lint-fix     - Run golangci-lint with auto-fix"
	@echo "  make install-linter - Install golangci-lint"
	@echo "  make install-hooks - Install pre-commit hooks (golangci-lint + gitleaks)"
	@echo "  make scan-secrets - Scan repository for secrets with gitleaks"
	@echo "  make test        - Run Go tests"
	@echo "  make build       - Build the project"
	@echo "  make build-mcp   - Build the coding-agent delegation MCP server"
	@echo "  make clean       - Clean build artifacts"
	@echo ""
	@echo "GitHub Actions (local testing with act):"
	@echo "  make install-act - Install act (tool to run GitHub Actions locally)"
	@echo "  make test-ci     - Run all CI jobs locally"
	@echo "  make test-ci-job JOB=<job-name> - Run a specific CI job (e.g., JOB=lint)"

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
	@go build -o bin/llm-test ./cmd/llm-test

# Build chat CLI
build-chat:
	@echo "Building chat CLI..."
	@go build -o bin/llm-chat ./cmd/llm-chat

# Build coding-agent delegation MCP server
build-mcp:
	@echo "Building coding-agent delegation MCP server..."
	@mkdir -p bin
	@go build -o bin/llm-provider-mcp ./cmd/llm-provider-mcp

# Build all binaries
build-all: build build-chat build-mcp
	@echo "All binaries built successfully"

# Install pre-commit hooks
install-hooks:
	@echo "Installing pre-commit hooks..."
	@chmod +x scripts/install-git-hooks.sh
	@./scripts/install-git-hooks.sh

# Scan for secrets
scan-secrets:
	@echo "Scanning for secrets..."
	@chmod +x scripts/scan-secrets.sh
	@./scripts/scan-secrets.sh

# Clean build artifacts
clean:
	@echo "Cleaning build artifacts..."
	@rm -rf bin/
	@go clean ./...

# Install act (tool to run GitHub Actions locally)
install-act:
	@echo "Installing act..."
	@if command -v brew > /dev/null; then \
		brew install act; \
	elif command -v curl > /dev/null; then \
		curl -s https://raw.githubusercontent.com/nektos/act/master/install.sh | sudo bash; \
	else \
		echo "Please install act manually: https://github.com/nektos/act#installation"; \
		exit 1; \
	fi
	@act --version
	@echo "✓ act installed successfully"
	@echo ""
	@echo "Next steps:"
	@echo "  1. Copy .secrets.example to .secrets: cp .secrets.example .secrets"
	@echo "  2. Run 'make test-ci' to test all CI jobs locally"

# Test CI workflows locally with act
test-ci: install-act
	@echo "Running CI workflow locally with act..."
	@echo "⚠️  Note: Act can be slow on first run (pulling Docker images)"
	@echo "⚠️  Output may not appear immediately - be patient..."
	@if [ ! -f .secrets ]; then \
		echo "⚠️  .secrets file not found. Creating from example..."; \
		cp .secrets.example .secrets; \
		echo "✓ Created .secrets file with placeholder values"; \
	fi
	@act push --container-architecture linux/amd64 --secret-file .secrets

# Test a specific CI job locally
test-ci-job: install-act
	@if [ -z "$(JOB)" ]; then \
		echo "❌ Error: JOB parameter is required"; \
		echo "Usage: make test-ci-job JOB=<job-name>"; \
		echo "Available jobs: lint, security-scan, test, build"; \
		exit 1; \
	fi
	@echo "Running CI job '$(JOB)' locally with act..."
	@echo "⚠️  Note: Act can be slow on first run (pulling Docker images)"
	@echo "⚠️  Output may not appear immediately - be patient..."
	@if [ ! -f .secrets ]; then \
		echo "⚠️  .secrets file not found. Creating from example..."; \
		cp .secrets.example .secrets; \
		echo "✓ Created .secrets file with placeholder values"; \
	fi
	@act push -j $(JOB) --container-architecture linux/amd64 --secret-file .secrets

# List available workflows and jobs
list-ci-jobs: install-act
	@echo "Available CI workflows and jobs:"
	@act -l
