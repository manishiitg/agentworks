# MCP Agent Go - 3-Agent Orchestrator System

A sophisticated Go implementation of an MCP (Model Context Protocol) client featuring a **complete 3-agent orchestrator system** that combines intelligent planning, tool execution, and fact-checking validation for enterprise-grade AI workflows.

## 🎯 **3-Agent Orchestrator Architecture**

The system implements a **complete sequential workflow** with three specialized agents:

### **🏗️ Planning Agent**
- **Role**: Strategic planner that creates comprehensive execution plans
- **Purpose**: Breaks down complex objectives into executable steps
- **Capabilities**:
  - Multi-step plan generation with dependencies
  - MCP server assignment for each step
  - Structured output with clear execution instructions
  - Adaptive planning based on previous executions

### **⚡ Execution Agent**
- **Role**: Operational executor that runs planned steps using MCP tools
- **Purpose**: Executes plans using real-world tools and APIs
- **Capabilities**:
  - Multi-MCP server integration (AWS, GitHub, Database, Kubernetes, etc.)
  - Tool execution with proper error handling
  - Result aggregation and formatting
  - Large output handling with file generation

### **🔍 Validation Agent**
- **Role**: Quality assurance agent that validates execution results
- **Purpose**: Prevents hallucinations and ensures factual accuracy
- **Capabilities**:
  - Fact-checking against external sources
  - Assumption detection and verification
  - Confidence scoring and evidence validation
  - False positive identification

## 🚀 **Complete Workflow: Planning → Execution → Validation**

```
🎯 User Request → 🏗️ Planning Agent → ⚡ Execution Agent → 🔍 Validation Agent → 📊 Final Report
    ↓                ↓                     ↓                    ↓                   ↓
Complex Objective → Structured Plan → Tool Execution → Fact-Checking → Validated Results
```

## Features

- **🧠 Intelligent Planning**: LLM-powered strategic planning with adaptive execution
- **🔧 Multi-MCP Integration**: 12+ MCP servers supporting AWS, GitHub, Database, Kubernetes, and more
- **✅ Fact-Checking Validation**: Prevents hallucinations with external validation
- **📊 Comprehensive Observability**: Full event tracing with Langfuse integration
- **⚡ Production Ready**: Enterprise-grade error handling and monitoring
- **🔄 Iterative Execution**: Plans evolve based on execution results
- **🎯 Configurable Agent Modes**: Simple vs. ReAct reasoning modes
- **🔧 Separate Structured Output**: Dedicated LLMs for planning vs. execution

### **Quick Start with 3-Agent Orchestrator**

#### **Complete Orchestrator Flow Test**
```bash
# Run the full 3-agent orchestrator flow
../bin/orchestrator test orchestrator-flow --log-file logs/3-agent-flow.log

# This executes: Planning → Execution → Validation
# Test Objective: "In-depth AWS Security Audit with Validation"
```

#### **Server Mode with Orchestrator**
```bash
# Use the production script (recommended)
./run_server_with_logging.sh

# This automatically configures:
# - Agent Mode: simple (reliable, no reasoning loops)
# - Structured Output: OpenAI GPT-4o (excellent JSON generation)
# - Main LLM: AWS Bedrock Claude Sonnet 4 (reasoning)
# - All 3 agents: Planning + Execution + Validation
```

#### **Manual Configuration (SQLite)**
```bash
# Start server with custom orchestrator settings using SQLite (default)
go run main.go server \
    --agent-mode simple \
    --structured-output-provider openai \
    --structured-output-model gpt-4o \
    --provider bedrock \
    --model "us.anthropic.claude-sonnet-4-20250514-v1:0" \
    --db-type sqlite \
    --db-path "/app/chat_history.db" \
    --log-file logs/orchestrator-server.log
```

#### **Manual Configuration (PostgreSQL)**
```bash
# Start server using PostgreSQL (Supabase)
export DATABASE_URL="postgres://user:password@host:port/dbname?sslmode=disable"
go run main.go server \
    --agent-mode simple \
    --db-type postgres \
    --log-file logs/orchestrator-server.log
```

### **Database Migration (SQLite to PostgreSQL)**

If you have existing data in SQLite and want to migrate to PostgreSQL (Supabase), use the provided migration tool:

1. **Build the migration tool**:
   ```bash
   cd agent_go
   go build -o ../bin/migrate-db ./cmd/migrate_db
   ```

2. **Run the migration**:
   ```bash
   # Using environment variable for Postgres URL
   export DATABASE_URL="postgres://user:password@host:port/dbname?sslmode=disable"
   ../bin/migrate-db -sqlite "chat_history.db"

   # Or explicitly passing the flag
   ../bin/migrate-db -sqlite "chat_history.db" -postgres "postgres://..."
   ```

   The tool will migrate:
   - Preset Queries (merging with existing ones based on ID)
   - Chat Sessions (skipping duplicates)
   - Events (skipping duplicates)
   - Workflows (skipping duplicates)
   - Slack Configuration (if enabled)

## Prerequisites

1. **AWS Credentials**: Configure AWS credentials for Bedrock access
   ```bash
   aws configure
   # or set environment variables:
   export AWS_ACCESS_KEY_ID=your_key
   export AWS_SECRET_ACCESS_KEY=your_secret
   export AWS_REGION=ap-southeast-1  # or your preferred region
   ```

2. **Node.js**: Required for MCP servers (npm packages)
   ```bash
   # Install Node.js if not already installed
   npm install -g @modelcontextprotocol/server-filesystem
   npm install -g @modelcontextprotocol/server-memory
   ```

## Installation

```bash
# Clone and build
cd agent_go
go build -o mcp-agent .
```

## Configuration

MCP servers are configured in `configs/mcp_servers.json`:

```json
{
  "mcpServers": {
    "filesystem": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "./demo"],
      "description": "File system access for testing"
    },
    "memory": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-memory"],
      "description": "Persistent memory storage"
    }
  }
}
```

## 🎯 **Orchestrator Usage**

### **Complete 3-Agent Orchestrator Flow**

#### **Test the Full Orchestrator Pipeline**
```bash
# Run complete Planning → Execution → Validation workflow
../bin/orchestrator test orchestrator-flow --log-file logs/3-agent-flow.log

# What it does:
# 1. 🏗️ Planning Agent: Creates structured execution plan
# 2. ⚡ Execution Agent: Executes plan using MCP tools
# 3. 🔍 Validation Agent: Validates results and prevents hallucinations
# 4. 📊 Final Report: Comprehensive validated results
```

#### **Server Mode with Orchestrator API**
```bash
# Start server with orchestrator support
./run_server_with_logging.sh

# The server now supports orchestrator queries via API:
# POST /api/query with orchestrator workflow requests
```

#### **Individual Agent Testing**
```bash
# Test only planning agent
../bin/orchestrator test orchestrator-planning --log-file logs/planning-only.log

# Test planning + execution (no validation)
# (Use the flow test above)
```

### **Traditional MCP Agent Usage**

#### **Interactive Chat Mode**
Start an interactive chat session with Claude using MCP tools:

```bash
./mcp-agent mcp-agent bedrock-chat filesystem
```

Commands available in chat mode:
- `help` - Show available commands
- `tools` - List available MCP tools
- `exit` - Exit the chat

#### **Single Question Mode**
Ask a single question and get a response:

```bash
./mcp-agent mcp-agent bedrock-run filesystem "What files are in the current directory?"
./mcp-agent mcp-agent bedrock-run memory "Store a note saying 'Meeting at 3pm' and then list all notes"
```

### Command Options

Both modes support these flags:

- `--model`: Bedrock model ID (default: `apac.anthropic.claude-sonnet-4-20250514-v1:0`)
- `--temperature`: Model temperature 0.0-1.0 (default: `0.2`)
- `--tool-choice`: Tool choice strategy - `auto`, `none`, `required`, or specific tool name (default: `auto`)
- `--max-turns`: Maximum conversation turns to prevent infinite loops (default: `30`)
- `--config`: Path to MCP server configuration (default: `configs/mcp_servers.json`)

### Examples

```bash
# Use a different model
./mcp-agent mcp-agent bedrock-run --model anthropic.claude-3-sonnet-20240229-v1:0 filesystem "List files"

# Force tool usage
./mcp-agent mcp-agent bedrock-run --tool-choice required memory "What do you remember?"

# Higher creativity
./mcp-agent mcp-agent bedrock-chat --temperature 0.7 filesystem

# Custom configuration
./mcp-agent mcp-agent bedrock-run --config my-servers.json myserver "Hello"
```

## Observability & Tracing

The agent includes comprehensive observability features for monitoring and debugging:

### Tracing Providers

Configure tracing via the `TRACING_PROVIDER` environment variable:

```bash
# Console tracing (development/debugging)
TRACING_PROVIDER=console ./mcp-agent mcp-agent bedrock-run filesystem "List files"

# Langfuse tracing (production monitoring)
TRACING_PROVIDER=langfuse ./mcp-agent mcp-agent bedrock-run filesystem "List files"

# No tracing (default)
./mcp-agent mcp-agent bedrock-run filesystem "List files"
```

### Langfuse Integration

For production observability, configure Langfuse credentials:

```bash
# Set Langfuse credentials
export LANGFUSE_PUBLIC_KEY=pk-lf-your-public-key
export LANGFUSE_SECRET_KEY=sk-lf-your-secret-key
export LANGFUSE_BASE_URL=https://cloud.langfuse.com  # optional
export TRACING_PROVIDER=langfuse

# Run with Langfuse tracing
./mcp-agent mcp-agent bedrock-run filesystem "What files are available?"
```

### Environment Configuration

The agent supports automatic `.env` file loading:

```bash
# Create .env file
cat > .env << EOF
LANGFUSE_PUBLIC_KEY=pk-lf-your-public-key
LANGFUSE_SECRET_KEY=sk-lf-your-secret-key
LANGFUSE_BASE_URL=https://cloud.langfuse.com
TRACING_PROVIDER=langfuse
LANGFUSE_DEBUG=true
EOF

# Credentials will be loaded automatically
./mcp-agent mcp-agent bedrock-run filesystem "List files"
```

### What Gets Traced

The observability system captures:

#### **Agent Initialization**
- MCP server connection and handshake
- Tool discovery and conversion
- Bedrock LLM initialization
- Configuration validation

#### **Conversation Flow**
- Complete conversation traces with hierarchical spans
- LLM generation spans with token usage metrics
- Tool execution spans with parameters and results
- Error handling and status tracking

#### **Detailed Metrics**
- **Token Usage**: Input/output/total tokens for each LLM call
- **Timing**: Precise start/end times for all operations  
- **Tool Parameters**: Complete tool call arguments and responses
- **Model Information**: Model ID, temperature, and other parameters

### Console Tracing Output

Console tracing provides rich, structured output:

```
08:52:04.021 [TRACE START] console-5 | agent-conversation (Go Bedrock) | {"question":"list files"}
08:52:04.021 [SPAN START] console-5 | agent-processing | null
08:52:04.021 [GEN START] parent=console-6 model=apac.anthropic.claude-sonnet-4-20250514-v1:0 | LLM Call
08:52:06.445 [GEN END] console-7 | {"usage":{"input":2390,"output":63,"total":2453,"unit":"TOKENS"}}
08:52:06.445 [SPAN START] console-6 | tool-execution-list_allowed_directories | {}
08:52:06.448 [SPAN END] console-8 | {"output":{"content":[{"type":"text","text":"Allowed directories..."}]}}
```

### Langfuse Dashboard

When using Langfuse, traces appear in the web dashboard with:

- **Hierarchical trace visualization** showing conversation flow
- **Token usage analytics** and cost tracking
- **Performance metrics** and timing analysis
- **Tool execution details** with full parameters and responses
- **Error tracking** and debugging information

### Implementation Details

The observability system features:

- **Shared State Pattern**: Mirrors Python Langfuse implementation for consistency
- **Background Processing**: Non-blocking event batching and transmission
- **Graceful Fallback**: Automatically falls back to console tracing if Langfuse fails
- **Production Ready**: Configurable batching, timeouts, and error handling
- **Thread Safe**: Proper mutex protection for concurrent access

## 🎯 **3-Agent Orchestrator: How It Works**

### **Complete Workflow Architecture**

#### **Phase 1: 🏗️ Intelligent Planning**
1. **Objective Analysis**: Planning Agent receives complex objective (e.g., "AWS Security Audit")
2. **Strategic Planning**: Uses LLM to break objective into executable steps
3. **Server Assignment**: Assigns appropriate MCP servers to each step
4. **Structured Output**: Generates JSON plan with dependencies and instructions

#### **Phase 2: ⚡ Tool Execution**
1. **Plan Parsing**: Execution Agent parses the structured plan
2. **Server Connection**: Connects to assigned MCP servers (AWS, GitHub, Database, etc.)
3. **Tool Execution**: Executes tools in sequence with proper error handling
4. **Result Aggregation**: Collects and formats all execution results

#### **Phase 3: 🔍 Validation & Fact-Checking**
1. **Result Analysis**: Validation Agent examines all execution results
2. **Assumption Detection**: Identifies assumptions made during execution
3. **External Validation**: Uses search tools to verify critical claims
4. **Confidence Scoring**: Provides confidence levels and evidence validation

#### **Phase 4: 📊 Final Report Generation**
1. **Result Synthesis**: Combines planning, execution, and validation results
2. **Professional Formatting**: Generates comprehensive markdown reports
3. **Evidence Integration**: Includes validation evidence and confidence scores
4. **Actionable Recommendations**: Provides remediation steps and priorities

### **Traditional MCP Agent: How It Works**

1. **Connection**: Connects to the specified MCP server and discovers available tools
2. **Tool Conversion**: Converts MCP tool definitions to Bedrock-compatible format
3. **System Prompt**: Builds a system prompt that instructs Claude on available tools
4. **Tool Calling Loop**:
   - Sends user question to Claude with available tools
   - If Claude wants to use tools, executes them via MCP
   - Sends tool results back to Claude
   - Repeats until Claude provides a final answer
5. **Response**: Returns Claude's final response to the user

## 🔧 **Supported MCP Servers**

The 3-agent orchestrator integrates with **12+ MCP servers** across multiple protocols (HTTP, SSE, stdio):

### **Production Servers (Orchestrator Ready)**
- **AWS Services**: `example-aws-mcp` - Complete AWS ecosystem (EC2, S3, IAM, CloudWatch, etc.)
- **GitHub Integration**: `mcp-github` - Repository analysis, security alerts, code review
- **Database Security**: `mcp-database` - Multi-database security assessment and monitoring
- **Kubernetes**: `mcp-kubernetes` - Cluster security, pod analysis, RBAC review
- **Grafana**: `mcp-grafana` - Monitoring and alerting integration
- **Sentry**: `mcp-sentry` - Error tracking and performance monitoring
- **Slack**: `mcp-slack` - Team communication and notification
- **Profiler**: `mcp-profiler` - Performance profiling and optimization

### **Utility Servers**
- **File System**: `@modelcontextprotocol/server-filesystem` - File operations and management
- **Memory**: `@modelcontextprotocol/server-memory` - Persistent knowledge graph
- **Search**: `tavily-mcp` - External web search and fact-checking
- **Scripts**: `example-scripts-mcp` - Custom script execution and automation

### **Server Capabilities**
- **🔒 Security Assessment**: AWS IAM, VPC, security groups, encryption
- **📊 Monitoring**: CloudWatch metrics, Grafana dashboards, Sentry alerts
- **🔍 Code Analysis**: GitHub security scanning, dependency review
- **🗄️ Data Security**: Database access controls, encryption, audit logging
- **☸️ Container Security**: Kubernetes RBAC, pod security, network policies
- **🔍 Validation**: External search, fact-checking, evidence gathering

## 🧪 **Orchestrator Testing**

### **Complete 3-Agent Flow Test**
```bash
# Test the full Planning → Execution → Validation pipeline
../bin/orchestrator test orchestrator-flow --log-file logs/3-agent-flow.log

# Expected output:
# ✅ Planning Agent: Creates structured execution plan
# ✅ Execution Agent: Executes plan using MCP tools
# ✅ Validation Agent: Validates results and prevents hallucinations
# ✅ Final Report: Comprehensive validated results
```

### **Individual Component Tests**
```bash
# Test only planning agent
../bin/orchestrator test orchestrator-planning --log-file logs/planning-only.log

# Test MCP server connections
../bin/orchestrator test aws-test --config configs/mcp_servers_clean.json

# Test structured output generation
../bin/orchestrator test structured-output --log-file logs/structured-test.log
```

### **Performance and Validation Tests**
```bash
# Test with different LLM providers
../bin/orchestrator test orchestrator-flow --provider bedrock --log-file logs/bedrock-test.log
../bin/orchestrator test orchestrator-flow --provider openai --log-file logs/openai-test.log

# Test with different agent modes
../bin/orchestrator test orchestrator-flow --agent-mode simple --log-file logs/simple-test.log
../bin/orchestrator test orchestrator-flow --agent-mode react --log-file logs/react-test.log
```

### **Debugging and Monitoring**
```bash
# Enable verbose logging
../bin/orchestrator test orchestrator-flow --log-file logs/debug.log --debug

# Monitor Langfuse traces
../bin/orchestrator test langfuse get --log-file logs/traces.log

# Check MCP server health
../bin/orchestrator test connection-pool --log-file logs/health.log
```

## Development

### Code Linting

This project uses [golangci-lint](https://golangci-lint.run/) for code quality and style enforcement.

#### Installation

```bash
# Install golangci-lint (if not already installed)
make install-linter

# Or install manually
curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(go env GOPATH)/bin latest
```

#### Running the Linter

```bash
# Run linter to check code quality
make lint

# Run linter with auto-fix for issues that can be automatically fixed
make lint-fix

# Or run directly
golangci-lint run ./...
```

#### Configuration

The linter configuration is in `.golangci.yml`. Key features:
- **Enabled Linters**: 50+ linters including gofmt, govet, staticcheck, gosec, and more
- **Exclusions**: Test files have relaxed rules, cache/bin directories are excluded
- **Auto-fix**: Many formatting issues can be automatically fixed
- **Performance**: Configured with 5-minute timeout and caching

#### Pre-commit Integration

Consider adding linting to your git workflow:

```bash
# Add to .git/hooks/pre-commit
#!/bin/sh
make lint
```

### Running Tests

```bash
# Unit tests
go test ./cmd -v

# Include integration tests (requires MCP servers)
go test ./cmd -v -timeout 60s

# Or use make
make test
```

### Project Structure

```
agent_go/
├── cmd/
│   ├── root.go           # Root command
│   ├── mcp_agent.go      # Main MCP agent commands
│   └── mcp_agent_test.go # Tests
├── pkg/mcpclient/
│   ├── client.go         # MCP client wrapper
│   ├── config.go         # Configuration loading
│   ├── tool.go           # Tool display utilities
│   └── tool_convert.go   # Tool conversion helpers
└── configs/
    └── mcp_servers.json  # MCP server configurations
```

### Adding New MCP Servers

1. Add server configuration to `configs/mcp_servers.json`
2. Ensure the server command is available (install via npm if needed)
3. Test connection: `./mcp-agent mcp connect your-server-name`

## Troubleshooting

### AWS Bedrock Access

- Ensure you have access to Claude models in your AWS region
- Check AWS credentials are properly configured
- Verify the model ID is available in your region

### MCP Server Issues

- Check if the MCP server command is available: `npx @modelcontextprotocol/server-filesystem --help`
- Verify Node.js is installed and npm packages are available
- Check server logs for connection issues

### Tool Calling Issues

- Use `--tool-choice auto` to let Claude decide when to use tools
- Use `--tool-choice required` to force tool usage
- Check tool argument formats match the MCP server expectations

## 📈 **Project Status & Roadmap**

### **✅ Current Status: PRODUCTION READY**
- **🏗️ 3-Agent Orchestrator**: Complete Planning → Execution → Validation workflow
- **🔧 Multi-MCP Integration**: 12+ servers across HTTP, SSE, and stdio protocols
- **✅ Fact-Checking Validation**: Prevents hallucinations with external validation
- **📊 Enterprise Observability**: Full Langfuse tracing and event monitoring
- **⚡ Production Performance**: Optimized for reliability and speed

### **🎯 Key Achievements**
- **Complete Sequential Workflow**: Professional-grade multi-agent orchestration
- **Enterprise Security**: AWS, GitHub, Database, Kubernetes security assessment
- **Quality Assurance**: Automated validation and hallucination prevention
- **Production Monitoring**: Comprehensive tracing and performance metrics
- **Scalable Architecture**: Clean, maintainable, and extensible design

### **🚀 Future Enhancements**
- **🔍 Enhanced Validation**: Integration with additional fact-checking services
- **📊 Advanced Reporting**: Custom report templates and export formats
- **🔄 Workflow Templates**: Pre-built workflows for common use cases
- **🤖 Agent Marketplace**: Extensible agent ecosystem
- **📈 Performance Optimization**: Advanced caching and parallel execution

### **🤝 Contributing**
This project welcomes contributions to enhance the 3-agent orchestrator system:

1. **Agent Development**: Create new specialized agents
2. **MCP Server Integration**: Add support for new MCP servers
3. **Validation Enhancement**: Improve fact-checking and validation capabilities
4. **UI/UX Improvements**: Enhance the frontend experience
5. **Documentation**: Improve guides and examples

### **📞 Support & Contact**
- **Issues**: Report bugs and request features via GitHub Issues
- **Discussions**: Join community discussions for questions and ideas
- **Documentation**: Comprehensive guides in `/docs` directory

## 📚 Documentation

- [Runtime MCP Overrides](docs/runtime_mcp_overrides.md) - Dynamic MCP server configuration per workflow (output directories, env vars, args)

## License

This project is part of the mcp-city-mall repository. The 3-agent orchestrator system represents a significant advancement in AI workflow orchestration, providing enterprise-grade capabilities for complex multi-step processes with built-in quality assurance and validation. 