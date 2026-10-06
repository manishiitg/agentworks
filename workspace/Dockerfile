# Build stage - Debian so the binary links against glibc
FROM golang:1.25-bookworm AS builder

# Install ripgrep and gcc for CGO support
RUN apt-get update && apt-get install -y --no-install-recommends ripgrep gcc libc6-dev git && rm -rf /var/lib/apt/lists/*

# Set working directory
WORKDIR /app

# Copy go mod files first
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the application with CGO enabled for SQLite
ENV CGO_ENABLED=1
RUN go build -o planner .

# Final stage - Debian bookworm-slim (glibc, avoids musl compatibility issues)
FROM debian:bookworm-slim

# Install system packages
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    curl \
    wget \
    bash \
    jq \
    ripgrep \
    git \
    patch \
    python3 \
    python3-pip \
    python3-dev \
    python3-venv \
    python3-matplotlib \
    build-essential \
    chromium \
    ffmpeg \
    && curl -fsSL https://deb.nodesource.com/setup_20.x | bash - \
    && apt-get install -y --no-install-recommends nodejs \
    && rm -rf /var/lib/apt/lists/*

# Install agent-browser globally for browser automation. It uses the
# system-installed Chromium through AGENT_BROWSER_EXECUTABLE_PATH.
RUN npm install -g agent-browser@latest

# Set Chromium path for agent-browser (system Chromium on Debian).
ENV AGENT_BROWSER_EXECUTABLE_PATH=/usr/bin/chromium

# Configure pip: allow installs when Python is "externally managed" (PEP 668)
RUN pip3 install --break-system-packages pandas requests beautifulsoup4 lxml pyyaml python-dotenv Pillow openpyxl

# Set working directory
WORKDIR /app

# Copy binary from builder stage
COPY --from=builder /app/planner .

# Create docs and data directories; empty workspace-docs structure (Downloads, Chats, Plans)
RUN mkdir -p /app/workspace-docs /app/data \
  /app/workspace-docs/Downloads /app/workspace-docs/Chats /app/workspace-docs/Plans

# NOTE: Running as root to allow unshare with mount namespaces (requires CAP_SYS_ADMIN)
# The application itself uses BuildSafeEnvironment() to prevent secret leakage

# Expose port
EXPOSE 8080

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://localhost:8080/health || exit 1

# Run the application
CMD ["./planner", "server", "--port", "8080", "--docs-dir", "/app/workspace-docs"]
