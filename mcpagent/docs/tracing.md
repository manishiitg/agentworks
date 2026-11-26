# Tracing Implementation

This document describes the tracing implementation in mcpagent and how to use it with Langfuse and LangSmith.

## Overview

The mcpagent supports multiple observability backends for tracing LLM interactions, tool calls, and MCP server connections:

- **Langfuse**: Full-featured observability platform with cost tracking
- **LangSmith**: LangChain's observability platform with evaluation features

Both tracers follow the same subscriber pattern and can be used independently or simultaneously. The tracing captures:

- **Traces**: Top-level container for a complete agent interaction
- **GENERATION spans**: LLM generation calls with token usage and model info
- **Tool spans**: MCP tool calls with inputs and outputs
- **MCP Connection spans**: Server connection timing and status
- **MCP Discovery spans**: Server and tool discovery information

## Trace Hierarchy

```
Trace (query_...)
├── Agent Span (agent_<model>_<tools>)
│   └── Conversation Span (conversation_...)
│       ├── GENERATION (llm_generation_turn_<N>_<model>_<tools>)
│       │   └── Tool Span (tool_<server>_<tool_turn_<N>)
│       ├── GENERATION (llm_generation_turn_<N>_<model>_<tools>)
│       │   └── Tool Span (...)
│       └── GENERATION (final response with text output)
├── MCP Connection Span (mcp_connection_<server>)
└── MCP Discovery Span (mcp_discovery_<N>_servers_<M>_tools)
```

## Span Details

### GENERATION Spans

Captures LLM API calls with:

| Field | Description |
|-------|-------------|
| `name` | `llm_generation_turn_<turn>_<model>_<tool_count>_tools` |
| `model` | Model ID (e.g., `gemini-3-flash-preview`) |
| `startTime` | When LLM call started |
| `endTime` | When LLM call completed |
| `output` | Text content (null for tool-call-only responses) |
| `usage.input` | Prompt tokens |
| `usage.output` | Completion tokens |
| `usage.total` | Total tokens |

**Note**: When the LLM generates only tool calls (no text response), the `output` field is null. This is expected behavior - only the final response with actual text content will have output populated.

### Tool Spans

Captures MCP tool executions with:

| Field | Description |
|-------|-------------|
| `name` | `tool_<server>_<tool_name>_turn_<N>` |
| `startTime` | When tool call started |
| `endTime` | When tool call completed |
| `output.result` | Tool execution result |
| `output.duration` | Execution time |
| `output.error` | Error message (if failed) |

### MCP Connection Spans

Captures server connection with:

| Field | Description |
|-------|-------------|
| `name` | `mcp_connection_<server_name>` |
| `startTime` | Connection start time |
| `endTime` | Connection end time |
| `output.connection_time` | Total connection duration |
| `output.server_info.servers_count` | Number of servers connected |
| `output.server_info.tools_count` | Number of tools discovered |
| `output.server_info.cache_used` | Whether cache was used |

### MCP Discovery Spans

Captures tool discovery with:

| Field | Description |
|-------|-------------|
| `name` | `mcp_discovery_<N>_servers_<M>_tools` |
| `output.connected_servers` | Successfully connected servers |
| `output.failed_servers` | Failed server connections |
| `output.discovery_time` | Time taken for discovery |
| `output.tool_count` | Total tools discovered |

## Configuration

### Environment Variables

Set these in your `.env` file:

```bash
# Langfuse tracing
LANGFUSE_PUBLIC_KEY=pk-lf-...
LANGFUSE_SECRET_KEY=sk-lf-...
LANGFUSE_BASE_URL=https://us.cloud.langfuse.com  # Optional, defaults to cloud.langfuse.com

# LangSmith tracing
LANGSMITH_API_KEY=lsv2_pt_...
LANGSMITH_PROJECT=mcpagent                    # Optional, defaults to "default"
LANGSMITH_ENDPOINT=https://api.smith.langchain.com  # Optional
LANGSMITH_PROJECT_ID=eac64540-...             # Optional, UUID for API queries

```

### Attach configured tracers

Use `observability.GetTracers("langfuse,langsmith", logger)` to obtain configured tracers, or `GetTracerWithLogger` for one backend. Pass them through `RuntimeConfig.Tracers` when constructing the Go runtime. Agent definitions and sessions keep their normal execution path; tracing receives its events.

Inspect recorded runs through the configured backend's own UI or API. The library's tracing integrations remain available, including LangSmith. Keep credentials in the environment and out of tool output and source files.
