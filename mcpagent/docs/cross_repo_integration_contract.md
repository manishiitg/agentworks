# Cross-Repo Integration Contract (mcpagent)

> **The canonical cross-repo contract has moved.** It now lives in
> `mcp-agent-builder-go/agent_go/docs/cross_repo_integration_contract.md`
> and is the single source of truth for the 3-repo LLM pipeline.

```
mcp-agent-builder-go (HTTP API + frontend)        ← canonical contract lives here
  → mcpagent (this repo — middle layer)
    → multi-llm-provider-go (adapter + real CLI)
```

## What this repo owns (Boundary 2)

mcpagent sits between the HTTP API and the LLM adapters. It owns:

- **Streaming chunk handling**: `agent/llm_generation.go` — receives
  `StreamChunk` from the adapter, builds `StreamingChunkEvent` /
  `ToolCallStartEvent` / `ToolCallEndEvent` / etc., forwards through
  the registered listeners (bridge, builder, token tracker).
- **TokenUsageEvent emission**: builds the per-turn cost event with
  `GenerationInfo` carrying everything from `Additional` (cost, model,
  effective_model, etc.) generically copied through.
- **Session-id round-tripping**: extracts CLI session IDs from
  `GenerationInfo.Additional` and re-injects on the next turn via
  `WithResumeSessionID()`.
- **Provider-specific option fan-out**: per-provider option builders
  in `executeLLM()` (claude-code, codex-cli, cursor-cli,
  agy-cli, pi-cli).
- **Selected-model retries**: provider identity remains fixed during execution and adapter
  re-initialisation.
- **Error classification**: `classifyLLMError()` → typed error events.

## Where to look for cross-repo concerns

- **Cost tracking** (USD, effective model, ledger flow) → canonical doc, "Cost Tracking Contract" section
- **Inspector debug** (opt-in event sink, phases, store) → canonical doc, "Inspector Debug Contract" section
- **Integration areas IC-1 through IC-10** → canonical doc
- **What flows through which boundary** → canonical doc, "Boundaries" section

When in doubt, the **canonical doc is the truth**. Update there first; only
update this file when the change is purely orchestrator-internal.

## Per-area mcpagent test files

| Area | Test file | Count |
|---|---|---|
| IC-1 Config propagation | `agent/coding_agent_options_test.go` | 6 tests |
| IC-2 Streaming chunk flow | `agent/llm_generation_streaming_test.go` | 13 tests |
| IC-4 Session ID & resume | `agent/session_resume_integration_test.go` | 18 subtests |
| IC-5 Model metadata | `agent/llm_generation_streaming_test.go` | 2 tests |
| Selected-model execution | `agent/selected_model_test.go` | Legacy-config and quota regression tests |
| IC-7 Error classification | `agent/error_classification_test.go` | 44 subtests |
| IC-9 Multi-turn tool context | `agent/cli_tool_history_test.go` | 3 tests |
| IC-10 MCP bridge config | `agent/coding_agents_bridge_test.go` | 7 tests |

## Key Functions Under Contract

- `extractCodingAgentSessionIDs()` — IC-4 session ID key extraction
- `buildStructuredResumeOptions()` — IC-4 resume option injection
- `streamingManager.processChunks()` — IC-2 chunk routing
- `finishStreaming()` — IC-2 safe close + StreamingEndEvent with metadata
- `classifyLLMError()` — IC-7 error type classification chain
- `getEffectiveLLMConfig()` — selected provider/model configuration
- `BuildBridgeMCPConfig()` — IC-10 MCP bridge config generation
- CLI tool call ↔ JSON ↔ message reconstruction — IC-9 round-trip
