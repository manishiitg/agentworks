# =============================================================================
# Why Debian (not Alpine) for the agent image:
# - Alpine uses musl libc; many MCP servers (e.g. mcp-clickhouse/chdb) need glibc
#   and libintl (gettext). On Alpine this required workarounds and still broke.
# - docker-ce-cli is easy to install from Docker's apt repo on Debian; on Alpine
#   it lives in the community repo and needs extra setup.
# - Single glibc-based base (Debian) avoids native extension and symbol issues
#   for Python/Node MCPs. Builder is also Debian so the Go binary is glibc-linked.
# =============================================================================

# Build stage - Debian so the binary links against glibc and runs on the Debian runtime
FROM golang:1.25-bookworm AS builder

# Production intentionally follows the same dependency branches used by local
# development. Override these args only for an explicit rollback or bisect.
ARG MCPAGENT_REF=main
ARG MULTI_LLM_PROVIDER_REF=main

RUN apt-get update && apt-get install -y --no-install-recommends git ca-certificates gcc libc6-dev && rm -rf /var/lib/apt/lists/*

# Set working directory
WORKDIR /app

# Copy all module definitions first for dependency caching
COPY workspace/go.mod workspace/go.sum ./workspace/
COPY agent_go/go.mod agent_go/go.sum ./agent_go/

# go.work so workspace module resolves locally (agent_go depends on workspace).
# Bump this when the module go directives in agent_go/go.mod or workspace/go.mod change.
RUN printf 'go 1.25.0\nuse (\n\t./agent_go\n\t./workspace\n)\n' > /app/go.work
ENV GOWORK=/app/go.work

# Drop local-dev replace directives because sibling checkouts are not in the
# Docker build context, then resolve the same main branches local development
# uses. This prevents production from silently running old coding-agent input
# and durable-ack code while local tests exercise newer sibling checkouts.
# The refs remain build args so an emergency rollback can name an exact commit.
RUN cd /app/agent_go && \
    go mod edit -dropreplace=github.com/manishiitg/mcpagent && \
    go mod edit -dropreplace=github.com/manishiitg/multi-llm-provider-go && \
    GOWORK=off GOFLAGS=-mod=mod go get \
      github.com/manishiitg/mcpagent@${MCPAGENT_REF} \
      github.com/manishiitg/multi-llm-provider-go@${MULTI_LLM_PROVIDER_REF}

# Download dependencies (retry up to 3 times — proxy.golang.org can be flaky).
# mcpagent and multi-llm-provider-go come from the GitHub refs resolved above.
# Inline GOFLAGS=-mod=mod + GOWORK=off lets `go mod download` add missing checksums
# to go.sum for those modules, which aren't present in the committed go.sum because
# the developer's go.sum was generated with the local `replace` directives active.
# Scoped to this RUN — leaving -mod=mod set globally would break subsequent
# workspace-mode builds (workspace mode forbids -mod=mod).
WORKDIR /app/agent_go
RUN for i in 1 2 3; do GOFLAGS=-mod=mod GOWORK=off go mod download && break || echo "Retry $i..."; sleep 5; done

# Copy source code
WORKDIR /app
COPY workspace/ ./workspace/
COPY agent_go/ ./agent_go/
COPY playbooks/ ./playbooks/

# Full COPY overwrote the resolved go.mod. Reapply the production branch refs.
RUN cd /app/agent_go && \
    go mod edit -dropreplace=github.com/manishiitg/mcpagent && \
    go mod edit -dropreplace=github.com/manishiitg/multi-llm-provider-go && \
    GOWORK=off GOFLAGS=-mod=mod go get \
      github.com/manishiitg/mcpagent@${MCPAGENT_REF} \
      github.com/manishiitg/multi-llm-provider-go@${MULTI_LLM_PROVIDER_REF}
# Build from /app so go.work is found; output binary to agent_go dir (glibc-linked for Debian runtime).
# -p=2 caps the parallel compile-job count — the in-cluster buildkit pod runs on a memory-tight
# node and the default (NumCPU) compile fan-out OOM-killed the build. Slower but survivable.
RUN cd /app && CGO_ENABLED=1 GOOS=linux GOMEMLIMIT=3GiB go build -p=2 -ldflags="-w -s" -o /app/agent_go/mcp-agent ./agent_go

# Build mcpbridge binary, which proxies coding-agent tool calls to our API.
RUN cd /app && CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /app/agent_go/mcpbridge github.com/manishiitg/mcpagent/cmd/mcpbridge

# Runtime stage - Debian bookworm-slim (glibc, good package availability)
FROM debian:bookworm-slim

# Base + MCP runtime: curl/wget/bash for scripts; jq/ripgrep for tools; Python+uv for
# Python MCPs (e.g. uvx run mcp-clickhouse); gettext-base for libintl (chdb/native deps).
# gnupg for adding Docker's apt repo.
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    curl \
    wget \
    bash \
    tmux \
    jq \
    ripgrep \
    sqlite3 \
    ffmpeg \
    python3 \
    python3-pip \
    python3-venv \
    gettext-base \
    gnupg \
    && curl -fsSL https://download.docker.com/linux/debian/gpg | gpg --dearmor -o /usr/share/keyrings/docker-archive-keyring.gpg \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/docker-archive-keyring.gpg] https://download.docker.com/linux/debian bookworm stable" > /etc/apt/sources.list.d/docker.list \
    && apt-get update \
    && apt-get install -y --no-install-recommends docker-ce-cli \
    && curl -fsSL https://deb.nodesource.com/setup_20.x | bash - \
    && apt-get install -y --no-install-recommends nodejs \
    && apt-get install -y --no-install-recommends chromium \
    && apt-get install -y --no-install-recommends gcc libc6-dev \
    && rm -rf /var/lib/apt/lists/*
# docker-ce-cli: MCP configs can use "command": "docker", "args": ["run", "-i", "--rm", "mcp/sonarqube"] (mount /var/run/docker.sock in deployment).
# nodejs 20+: required for npx MCPs (mcp-remote/undici, @sylphx/pdf-reader-mcp, etc.). chromium: agent-browser. gcc/libc6-dev: CGO and native Python deps.

# agent-browser: version-matched CLI and bundled skills used by the managed
# workspace_browser tool. Native deployments execute it locally; container
# deployments also install it in the workspace image below.
RUN npm install -g agent-browser@latest && which agent-browser && agent-browser --version
COPY agent_go/scripts/install-slack-cli.sh /tmp/install-slack-cli.sh
RUN bash /tmp/install-slack-cli.sh /usr/local && rm /tmp/install-slack-cli.sh

# Install Google Chrome (stable, amd64). agent-browser will use this
# binary instead of the Chromium installed via apt above. We keep chromium too
# so any code path that hardcodes /usr/bin/chromium still works.
RUN curl -fsSL https://dl.google.com/linux/linux_signing_key.pub | gpg --dearmor -o /usr/share/keyrings/google-linux-signing-keyring.gpg && \
    echo "deb [arch=amd64 signed-by=/usr/share/keyrings/google-linux-signing-keyring.gpg] https://dl.google.com/linux/chrome/deb/ stable main" > /etc/apt/sources.list.d/google-chrome.list && \
    apt-get update && \
    apt-get install -y --no-install-recommends google-chrome-stable && \
    rm -rf /var/lib/apt/lists/* && \
    google-chrome --version

# Point agent-browser at the real Chrome binary by default.
ENV AGENT_BROWSER_EXECUTABLE_PATH=/usr/bin/google-chrome

# AWS CLI v2 (single static binary, no Python deps). Pod credentials come from
# EKS Pod Identity (AWS_CONTAINER_CREDENTIALS_FULL_URI + AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE
# auto-injected by the cluster's Pod Identity Agent for the bound ServiceAccount —
# `default` in prod-mcpagent per agent/deployment.yaml). The CLI picks them up automatically.
RUN apt-get update && apt-get install -y --no-install-recommends unzip && \
    curl -fsSL "https://awscli.amazonaws.com/awscli-exe-linux-$(uname -m).zip" -o /tmp/awscliv2.zip && \
    unzip -q /tmp/awscliv2.zip -d /tmp && \
    /tmp/aws/install && \
    rm -rf /tmp/aws /tmp/awscliv2.zip /var/lib/apt/lists/* && \
    aws --version

# kubectl: workflows that operate on EKS clusters (analyze-eks-utilization, etc.) need
# kubectl for port-forwards and `kubectl top`. Pod uses EKS Pod Identity for AWS auth;
# kubectl auth is bootstrapped at runtime via `aws eks update-kubeconfig`.
RUN curl -fsSL "https://dl.k8s.io/release/$(curl -fsSL https://dl.k8s.io/release/stable.txt)/bin/linux/amd64/kubectl" -o /usr/local/bin/kubectl && \
    chmod +x /usr/local/bin/kubectl && \
    kubectl version --client

# Optionally install Pi CLI. The adapter can fall back to npx, but installing
# the binary avoids first-run network latency in tmux-backed coding-agent runs.
ARG INSTALL_PI_CLI=false
RUN if [ "$INSTALL_PI_CLI" = "true" ]; then \
        npm install -g @earendil-works/pi-coding-agent && which pi && pi --version; \
    else \
        echo "Skipping @earendil-works/pi-coding-agent install (INSTALL_PI_CLI=$INSTALL_PI_CLI)"; \
    fi

# Install uv (uvx for Python MCP servers) and pypdf (agents extract PDF text
# themselves via execute_shell_command + python; there is no dedicated PDF tool)
RUN pip3 install --break-system-packages uv pypdf

# Verify npx and docker are available
RUN which npx && which docker && which tmux && npm --version && npx --version && docker --version

# Verify ripgrep, jq and sqlite3
RUN which rg && which jq && which sqlite3 && rg --version && jq --version && sqlite3 --version

# Create non-root user. Runtime caches (uvx/npx) land in /home/appuser/.cache/
RUN groupadd -g 1001 appgroup && useradd -m -u 1001 -g appgroup appuser

# MCP server packages (uvx/npx) are installed lazily on first connection at runtime.
# The ConnectTimeout (15 min) in mcpclient/client.go handles first-run install time.

# Set working directory
WORKDIR /app

# Copy the binaries from builder stage
COPY --from=builder /app/agent_go/mcp-agent .
COPY --from=builder /app/agent_go/mcpbridge /usr/local/bin/mcpbridge
COPY --from=builder /app/playbooks ./playbooks
ENV AGENTWORKS_PLAYBOOKS_DIR=/app/playbooks

# Copy Python scripts (PDF extraction, etc.)
COPY agent_go/scripts/extract_pdf_text.py /app/scripts/

# Copy Go from builder for code execution mode (run Go snippets)
COPY --from=builder /usr/local/go /usr/local/go
ENV GOROOT=/usr/local/go
ENV PATH="/usr/local/go/bin:${PATH}"
RUN go version

# Copy configuration files
COPY --from=builder /app/agent_go/configs ./configs

# Create reports and logs directories
RUN mkdir -p reports logs && chown -R appuser:appgroup /app

# Switch to non-root user
USER appuser

# Expose port
EXPOSE 8000

# Set default command
CMD ["./mcp-agent", "server", "--host", "0.0.0.0", "--port", "8000", "--log-file", "/app/logs/server_debug.log", "--log-level", "debug", "--mcp-config", "configs/mcp_servers_docker.json"]
