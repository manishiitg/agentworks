[← platform / coding-agent-bridge](index.md)

# PLAT-274 — Cursor structured transport on a locked deployment: bridge tools auto-rejected, thinking fragmented, sentences doubled, denials mis-classified

| Field | Value |
|---|---|
| State | deployed |
| Priority | P0 |
| Product | platform |
| Area | coding-agent-bridge |
| Summary | P0 — with AgentWorks locked to Cursor on RTS, every `execute_shell_command` a workflow step attempted was refused, so no step could act. |

| Coordination | Value |
|---|---|
| Assigned agent | Claude Code |
| Ticket state | `fixed` — deployed to RTS 2026-09-03 and verified live; see Verification |
| Last synchronized | `2026-09-03` |

- **Priority:** P0 — with AgentWorks locked to Cursor on RTS, every `execute_shell_command` a workflow step attempted was refused, so no step could act.
- **Owner:** `multi-llm-provider-go/pkg/adapters/cursorcli/cursorcli_structured_adapter.go`, `cursorcli_structured_args.go`; `mcpagent/agent/llm_generation.go`, `mcpagent/events/data.go`, `mcpagent/toolerr/toolerr.go`; frontend `utils/thinkingDeltas.ts`, `TerminalEventTranscript.tsx`.
- **Origin:** live, RTS `rtslatency` Builder chat (`cursor-cli/grok-4.6`, cursor-agent 2026.09.02), reported by the operator on 2026-09-03; not a Pulse finding.

## What the operator saw

1. `{"rejected":{"reason":"User rejected MCP: api-bridge-execute_shell_command","isReadonly":false}}` on every bridge shell call.
2. A column of one-line "Thinking" cards ("Reading soul.md first." / "Determining the current" / "phase from plan.json").
3. Every sentence of the streamed answer rendered twice, verbatim.
4. A denied built-in `read` shown as ✓ while a denied `glob` showed ✗, for the same deny-hook verdict.

## Root causes

1. The `--print` launcher withholds `--force` once the deny-builtin hooks are installed (correct: `--force` bypasses hooks) but, unlike the tmux path, never wrote `.cursor/cli.json` with `{"permissions":{"allow":["Mcp(<server>:*)"]}}`. Headless cursor-agent has nobody to answer its per-tool prompt for a non-read-only MCP tool and reports the call as user-rejected.
2. Cursor streams thinking as token fragments; the adapter emitted plain reasoning chunks and the mcpagent consumer made one `ConversationThinkingEvent` per chunk. Claude Code emits whole blocks, so this never showed before.
3. cursor-agent 2026.09.02 sends each assistant fragment with `timestamp_ms` and no subtype, then the assembled span with neither. The adapter told them apart by subtype only, so the repeat went out as one more delta.
4. `toolerr.CanonicalFailure` recognised neither `{"error":{"errorMessage":…}}` (read) nor `{"error":{"error":…}}` (glob); glob's ✗ came from cursor's own status.

## Fix

- `cursorStructuredCLIConfig` writes the same allowlist the tmux path projects (caller `MetadataKeyProjectConfig` wins verbatim) — multi-llm `4b7b02f`.
- Thinking fragments carry `ContentDeltaMetadataKey` (`8dc96d6`); `ConversationThinkingEvent.IsDelta` (mcpagent `59dd7ae`); the frontend folds deltas into the preceding thinking event (`a4a65103c`) and renders a collapsible "Thinking" block that minimises once answer text streams (`18c0da5a9`).
- Assembled repeat dropped when `timestamp_ms` is absent or the text equals the accumulated fragments; span state resets at a tool call (`063aedd`, test replays the probed shape).
- Error envelopes under an `error` field classify as `error.message` (mcpagent `f7b6927` + `d8b1f6b`).

## Verification

- Unit: `TestCursorStructuredCLIConfigPreapprovesInjectedMCPServers`, `TestCursorStructuredDropsAssembledSpanRepeat`, `TestCanonicalFailureRecognisesErrorEnvelopeMessageKeys`, frontend `thinkingDeltas.test.ts`, `thinkingTranscriptGroup.test.ts`.
- Live (RTS release `9d48c370c-20260903140555` and later): the operator's `hi` turn ran `execute_shell_command` through the bridge (43 ms, no rejection); a later turn streamed 74 chunks with no assembled repeats; thinking renders as one block.

## Lesson

Keep the tmux and structured adapters' `.cursor/` projections (mcp.json, cli.json allowlist, hooks, rules, skills) in lockstep, and probe real cursor-agent stream-json shapes (`jq '{type,subtype,keys}'`) after every CLI update rather than trusting field comments.

