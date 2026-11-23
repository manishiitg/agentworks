# Parallel Tool Execution

## Overview

When the LLM returns multiple tool calls in a single response, the agent can execute them concurrently using goroutines instead of processing them one at a time. This reduces wall-clock latency when multiple independent tool calls are issued together.

## Enabling

```go
runtime := mcpagent.RuntimeConfig{
    Tools: mcpagent.ToolRuntimeConfig{ParallelExecution: true},
}
// Pass runtime with your AgentDefinition to NewAgentFromDefinition.
```

Default: `false` (sequential execution).

## How It Works

The implementation uses a three-phase fork-join pattern in `agent/parallel_tool_execution.go`:

### Phase 1: Sequential Preparation

All tool calls are prepared sequentially:
- Parse tool arguments
- Resolve MCP clients
- Emit `ToolCallStartEvent` (with `IsParallel: true`)

This keeps event ordering deterministic.

### Phase 2: Parallel Execution

Each tool call runs in its own goroutine:

```go
results := make([]toolExecutionResult, len(plans))
var wg sync.WaitGroup

for i, plan := range plans {
    wg.Add(1)
    go func(idx int, p toolExecutionPlan) {
        defer wg.Done()
        results[idx] = executeToolCall(ctx, a, p, ...)
    }(i, plan)
}

wg.Wait()
```

Results are written to pre-allocated indexed slots (no mutex needed since each goroutine writes to a unique index).

### Phase 3: Sequential Assembly

Results are collected in deterministic order matching the original tool call order:
- Append tool response messages
- Emit `ToolCallEndEvent` / `ToolCallErrorEvent`
- Run loop detection

## Observability

`ToolCallStartEvent` includes an `IsParallel` field:

| Field | Type | Description |
|-------|------|-------------|
| `IsParallel` | `bool` | `true` when the tool call is part of a parallel execution batch, `false` for sequential execution |

This allows event listeners and tracers to distinguish between parallel and sequential tool calls without relying on timing heuristics.

### Example event data

```json
{
  "event_type": "tool_call_start",
  "turn": 2,
  "tool_name": "resolve_library_id",
  "is_parallel": true,
  "server_name": "context7"
}
```

## Testing

Run the credential-free routing regression, which checks that parallel preparation
never borrows another MCP server's client:

```sh
go test ./agent -run '^TestPrepareParallelToolExecutionDoesNotUseAnotherServersClientForLazyServer$' -count=1
```

For a live application check, issue independent tool calls in a session with
`RuntimeConfig.Tools.ParallelExecution` enabled and inspect the `IsParallel`
event field and ordered results.

## When to Use

Parallel execution is beneficial when:
- The LLM frequently issues multiple independent tool calls in one response
- Tool calls involve network I/O (MCP server calls, API requests)
- Latency reduction matters more than strict sequential ordering

It is safe to enable because:
- Results are always assembled in the original tool call order
- Each goroutine writes to an isolated result slot
- Event emission (start/end) is still sequential

## Key Files

| File | Purpose |
|------|---------|
| `agent/parallel_tool_execution.go` | Fork-join implementation |
| `agent/conversation.go:920` | Entry point — branches to parallel or sequential |
| `agent/agent.go` | `RuntimeConfig.Tools.ParallelExecution` |
| `events/data.go` | `ToolCallStartEvent.IsParallel` field |
| `agent/sequential_mcp_routing_test.go` | Routing regression |
