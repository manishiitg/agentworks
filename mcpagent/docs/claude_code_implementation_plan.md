# Claude Code CLI Integration Implementation Plan

## Goal
Integrate the Claude Code CLI adapter into the MCP Agent system (`mcpagent`), enabling the agent to use the local `claude` CLI as an LLM provider (`ProviderClaudeCode`) with native tool capabilities while disabling conflicting agent-side features.

## Architecture

1.  **Provider Registration**:
    *   Map `ProviderClaudeCode` to "claude-code" in `multi-llm-provider-go` and `mcpagent/llm`.
    *   This allows the system to recognize "claude-code" as a valid provider.

2.  **Configuration Passing**:
    *   The `Claude Code` CLI requires MCP server configuration to be passed as a JSON string via the `--mcp-config` flag.
    *   We implement `GetMCPConfigJSON()` in `mcpagent/agent/agent.go` to serialize the agent's current MCP configuration.
    *   We inject this config into the LLM execution call via `WithMCPConfig` option.

3.  **Feature Auto-Configuration**:
    *   The `Claude Code` CLI handles its own context management, tool discovery, and code execution.
    *   The `mcpagent`'s built-in implementations of these features conflict or are redundant.
    *   We automatically configure features in `NewAgent` and `NewAgentWithObservability` when `ProviderClaudeCode` is detected.

## Feature Compatibility Matrix

| Feature | Status | Reason |
|---|---|---|
| `Ask` / `AskWithHistory` | **Works** | Core chat functionality |
| Streaming | **Auto-enabled** | Required for tool call observability events |
| Tool Call Events (start/end) | **Works** | Parsed from CLI stream events with args, result, duration |
| Langfuse/LangSmith Tracing | **Works** | Events use standard `ToolCallStartEvent`/`ToolCallEndEvent` |
| Context Offloading | **Silently disabled** | CLI handles natively |
| Context Summarization | **Error if enabled** | CLI handles natively |
| Context Editing | **Error if enabled** | CLI handles natively |
| Code Execution Mode | **Error if enabled** | Not supported via CLI wrapper |
| Parallel Tool Execution | **Flag ignored** | CLI manages its own parallel execution internally |

## Implementation Details

### 1. `multi-llm-provider-go`

#### Adapter (`pkg/adapters/claudecode/claudecode_adapter.go`)
*   Accepts `WithMCPConfig`, `WithDangerouslySkipPermissions`, and `WithClaudeCodeTools` options.
*   Launches `claude` CLI with `--output-format stream-json --input-format stream-json --verbose --include-partial-messages`.
*   Streams user/assistant messages via stdin in stream-json format.
*   Parses stdout as a real-time JSON stream.

#### Stream Event Parsing (Tool Call Observability)
The adapter parses the CLI's `stream_event` JSON objects as a state machine:

1.  **`content_block_start`** with `type: tool_use` → Emits `StreamChunkTypeToolCallStart` with tool name and ID. Buffers a `pendingToolCall` with start time.
2.  **`content_block_delta`** with `input_json_delta` → Accumulates partial JSON arguments in a `strings.Builder`.
3.  **`content_block_stop`** → Saves accumulated args to the pending buffer. Does NOT emit `ToolCallEnd` yet (waits for result).
4.  **`"type": "user"`** with `tool_result` → Matches by `tool_use_id`, emits `StreamChunkTypeToolCallEnd` with:
    *   `ToolArgs` — complete JSON arguments
    *   `ToolResult` — tool execution result content
    *   `ToolDuration` — wall-clock time from start to result
5.  **`"type": "result"`** → Flushes any remaining pending tool calls as fallback (no result, duration only).

#### Playback Deduplication
*   Counts historical AI messages to skip them during playback streaming.
*   Skips consolidated `assistant` messages when `stream_event`s are already providing real-time deltas.

#### StreamChunk Types (`llmtypes/types.go`)
```go
const (
    StreamChunkTypeContent       StreamChunkType = "content"
    StreamChunkTypeToolCall      StreamChunkType = "tool_call"
    StreamChunkTypeToolCallStart StreamChunkType = "tool_call_start"
    StreamChunkTypeToolCallEnd   StreamChunkType = "tool_call_end"
)

type StreamChunk struct {
    Type         StreamChunkType
    Content      string
    ToolCall     *ToolCall
    ToolName     string
    ToolCallID   string
    ToolArgs     string          // Complete JSON arguments
    ToolResult   string          // Tool execution result
    ToolDuration time.Duration   // Duration from start to result
}
```

### 2. `mcpagent`

#### Provider Registration (`llm/providers.go`)
*   Added `ProviderClaudeCode`.

#### Auto-Configuration (`agent/agent.go`)
In both `NewAgent` and `NewAgentWithObservability`:
*   **Errors** for: `UseCodeExecutionMode`, `EnableContextEditing`, `EnableContextSummarization`
*   **Silently disables**: `EnableContextOffloading`
*   **Auto-enables**: `EnableStreaming` (required for tool call observability)

#### Tool Event Bridge (`agent/llm_generation.go`)
`processChunks` in `streamingManager` handles:
*   `StreamChunkTypeToolCallStart` → Emits `ToolCallStartEvent` via `EmitTypedEvent` with turn, tool name, correlation IDs.
*   `StreamChunkTypeToolCallEnd` → Emits `ToolCallEndEvent` via `EmitTypedEvent` with tool name, result, duration, model ID, and ToolCallID for correlation.

These events use the exact same `AgentEvent` structures as native tool execution, so all downstream consumers (Langfuse spans, event listeners, frontend EventDisplay) work without changes.

#### MCP Config Injection (`agent/llm_generation.go`)
*   `executeLLM` injects the MCP config via `WithMCPConfig` call option.

## Testing

### Coding-agent continuation checks

Run `make test-p0-resume` for the credential-free Go runtime continuation contract. Adapter regressions live with the coding-agent provider implementations.

### Unit Test (`multi-llm-provider-go`)
```bash
cd multi-llm-provider-go
go test -v ./pkg/adapters/claudecode -run TestClaudeCodeStreaming
```

## Known Limitations

### HTTP/SSE MCP Servers
The `claude` CLI cannot dynamically bootstrap remote HTTP/SSE based MCP servers via `--mcp-config` in headless mode (`-p`).

*   **Behavior**: Injecting HTTP config causes the non-interactive CLI to hang and time out.
*   **Root Cause**: The CLI expects HTTP MCPs to be configured interactively.
*   **Workaround**: Use `stdio` based local servers (e.g., `@modelcontextprotocol/server-memory`).

### Nested Sessions
Claude Code CLI cannot be launched inside another Claude Code session. The `CLAUDECODE` environment variable must be unset for testing.

### Tool Results
Tool results are captured from the CLI's internal `tool_result` messages. If a tool call is interrupted or the CLI exits before returning a result, the `ToolCallEnd` event will be emitted without a result (fallback from the `"type": "result"` flush).

## Files Modified

### `multi-llm-provider-go`
*   `llmtypes/types.go` — StreamChunk types and fields
*   `pkg/adapters/claudecode/claudecode_adapter.go` — Stream parsing, tool buffering, result matching
*   `pkg/adapters/claudecode/claudecode_stream_integration_test.go` — Integration test

### `mcpagent`
*   `llm/providers.go` — `ProviderClaudeCode` registration
*   `agent/agent.go` — Auto-configuration block and `GetMCPConfigJSON`
*   `agent/llm_generation.go` — `processChunks` tool event bridge, `executeLLM` MCP config injection
