# Coding SDK Structured (JSON Streaming) Contract

This document defines the structured JSON streaming transport contract for
coding SDK providers in `multi-llm-provider-go`.

The installed provider contract is authoritative: Codex uses app-server, Cursor
uses ACP, and Pi uses RPC. Claude keeps tmux by default and has two explicit
structured paths: bounded workflow JSON and the official Agent SDK.

For Claude SDK mode set `CLAUDE_CODE_TRANSPORT=sdk` in the backend environment
and restart it. `CLAUDE_CODE_TRANSPORT=tmux` retains the terminal mode. Install
Node.js and `npm install -g @anthropic-ai/claude-agent-sdk@0.3.311` first, or set
`CLAUDE_AGENT_SDK_PATH` to the absolute `sdk.mjs` of an existing installation.
No packages are downloaded during a chat request. This switch affects Claude only.

SDK mode uses streaming input/output through the actual Anthropic package, native
question callbacks and acknowledged interruption. Each platform turn owns its
process; subsequent turns use the same native session via resume. Messages sent
while a query is running use native priority-now steering, acknowledged by their
CLI message UUID. The CLI can move active tools aside to take the new instruction.
If the query has ended before submission, the host queues a follow-up normally.
Terminal controls are absent in SDK mode.
Existing account scope, MCP/native tool policy, OS confinement, transcript files,
child-transcript reader and usage accounting apply. Native plan usage is optional
and not advertised by this mode. Existing bounded workflow JSON stays available
when SDK mode is not selected.

The CLI command examples below describe the earlier bounded JSON adapters;
current Codex/Cursor runtime launch contracts are app-server/ACP respectively.

## Provider CLI Commands

| Provider | Structured CLI | Key Flags |
|---|---|---|
| Claude Code | `claude -p --output-format stream-json` | `--system-prompt-file`, `--mcp-config`, `--tools ""` |
| Codex CLI | `codex exec --json` | `--model`, `--config` |
| Cursor CLI | `cursor-agent --print --output-format stream-json --stream-partial-output` | `--trust`, `--force`, `--workspace`, `--model`, `--mode` |

## Event Formats by Provider

### Claude Code (stream-json)

```jsonl
{"type":"message_start","message":{"id":"msg_...","model":"claude-haiku-4-5-20241022",...}}
{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}
{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}
{"type":"content_block_stop","index":0}
{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":12}}
{"type":"message_stop"}
{"type":"result","subtype":"success","result":"...","session_id":"...","usage":{"input_tokens":1234,"output_tokens":56}}
```

### Codex CLI (exec --json)

```jsonl
{"type":"message","message":{"role":"assistant","content":[{"type":"output_text","text":"Hello"}]}}
{"type":"completed","response":{"output":[...],"usage":{"input_tokens":100,"output_tokens":50}}}
```

### Cursor CLI (stream-json)

```jsonl
{"type":"system","subtype":"init","session_id":"...","model":"...","permissionMode":"..."}
{"type":"user","message":{"role":"user","content":[{"type":"text","text":"..."}]}}
{"type":"thinking","subtype":"delta","text":"...","session_id":"..."}
{"type":"thinking","subtype":"completed","session_id":"..."}
{"type":"tool_call","subtype":"started","call_id":"...","tool_call":{...}}
{"type":"tool_call","subtype":"completed","call_id":"...","tool_call":{...}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"delta"}]}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"full final text"}]}}
{"type":"result","subtype":"success","result":"...","session_id":"...","usage":{"inputTokens":3705,"outputTokens":45,"cacheReadTokens":7040}}
```

## Normative Structured Agent Contract

### 1. Launch

The adapter must:

- find the provider CLI binary via PATH or configured location
- pass the structured output flag (`--print --output-format stream-json`,
  `exec --json`, `run --format json`, etc.)
- pass tool approval flags (`--force`, `--trust`,
  `--dangerously-skip-permissions`) since there is no interactive user
- pass `--workspace <dir>` or `--dir <dir>` when a working directory is set
- pass `--model <model>` when a model override is set
- set the process group for clean cancellation
- build the environment with provider-specific API keys
- report clear errors for missing binary, auth, or version

### 2. Instructions and Prompting

The adapter must:

- extract system messages from the message list
- route system instructions through the provider-native mechanism when
  available (Claude Code `--system-prompt-file`, Cursor `.cursor/rules`)
- fall back to prepending system instructions to the prompt when no native
  mechanism exists
- build user conversation from all non-system messages
- reject empty prompts with a clear error

### 3. Tool Surface

The adapter must:

- pass tool approval flags so all tool calls execute without interactive
  approval
- parse tool-call events from the JSON stream and stream them as
  `StreamChunkTypeToolCallStart` and `StreamChunkTypeToolCallEnd` chunks
- support MCP bridge configuration when the provider supports it
- support bridge-only mode (disable built-in tools) via provider-specific
  flags (`--tools ""`, `--mode ask`, policy files)

### 4. Response Extraction

The adapter must:

- accumulate text/assistant content events into the final response
- for providers with streaming deltas plus a final full-text event (Cursor),
  prefer the final full-text event
- trim whitespace from the accumulated content
- return an error if no text was received and the process exited with error
- map provider-specific stop reasons to `stop` or `tool_calls`

### 5. Token Usage

The adapter must:

- extract token usage from the provider's result/finish events
- normalize to `InputTokens`, `OutputTokens`, `TotalTokens`
- propagate `CacheTokens` when the provider reports cache read/write
- sum across multiple step/result events for multi-step agent runs
- **also surface cache totals in `gi.Additional` under the raw
  Anthropic-style keys `cache_read_input_tokens` and (Anthropic
  only) `cache_creation_input_tokens`** — the cost-ledger
  pipeline reads these from Additional, not from the typed
  `CachedContentTokens` field. See
  [`docs/COSTS_AND_CONVERSATION_HISTORY.md`](COSTS_AND_CONVERSATION_HISTORY.md)
  → "Cache token surfacing contract".

### 6. Session Metadata

The adapter must:

- capture the session/thread/request ID from the provider events
- expose it in `GenerationInfo.Additional` with a provider-specific key
- expose the transport mode (`structured`, `stream-json`, `exec-json`, etc.)

### 7. Streaming

The adapter must:

- stream assistant text deltas as `StreamChunkTypeContent` chunks
- stream thinking/reasoning deltas as `StreamChunkTypeReasoning` chunks when
  available
- stream tool calls as `StreamChunkTypeToolCallStart` and
  `StreamChunkTypeToolCallEnd` chunks
- close the stream channel after process exit or error
- use non-blocking sends to avoid deadlock

### 8. Error Handling

The adapter must:

- return clear errors for missing binary
- include stderr output in error messages when the process fails
- return an error if the process exits with non-zero and no text was captured
- handle malformed JSON lines gracefully (skip and log)
- close the stream channel on all error paths

### 9. Process Shutdown Contract

Every structured CLI subprocess MUST be torn down by the adapter using
this exact sequence once a **terminal-state** event (`result` /
`task_complete` / `done` / equivalent) is observed on stdout:

```
SIGTERM  →  10s  →  SIGTERM  →  10s  →  SIGTERM  →  5s  →  SIGKILL
```

Up to **25s** total grace, three SIGTERMs, then unconditional SIGKILL.

Adapters MUST use the shared helper
`pkg/adapters/internal/procshutdown.Graceful(cmd, terminated, logger)`
rather than open-coding the sequence. The helper owns the timing policy
— call sites pass no duration. The helper:

1. Sends `SIGTERM #1` to the process group (`-pid`, requires `Setpgid: true`).
2. Waits up to `FirstGrace` (10s) for `terminated` to close.
3. If still alive: sends `SIGTERM #2`, waits up to `SecondGrace` (10s).
4. If still alive: sends `SIGTERM #3`, waits up to `ThirdGrace` (5s).
5. If still alive: sends `SIGKILL` to the process group.
6. Returns once the kill has been issued. The adapter's main goroutine
   remains responsible for `cmd.Wait()` and reaping — via
   `procshutdown.Reap`, never a bare `cmd.Wait()`. See "Completion is not
   guaranteed to arrive on stdout" below.

The helper is launched as a goroutine from the decode loop so the scanner
keeps draining stdout while shutdown is in progress.

**Why three SIGTERMs.** Most well-behaved CLIs exit on the first SIGTERM
and the second / third are never sent. The repeats exist for CLIs whose
event loop is briefly starved (e.g. Node.js CLIs blocked in a synchronous
HTTP call to their MCP bridge) — the second / third SIGTERM lands on a
warmer loop and tends to be serviced. Each signal also writes a distinct
log line, so operators can read the logs and see which CLI needed how
many nudges. After three attempts we stop being polite and SIGKILL.

**What the graces are for.** The total 25s window lets the CLI flush
state the *next* call depends on:

- session files used by `--resume` (`~/.gemini`, `~/.claude`, `~/.codex`,
  `~/.cursor`, …)
- transcript JSONL used for cost reconciliation
- any provider-specific rollout state

Token usage and pricing data MUST already be captured from the terminal
event itself — not from on-disk transcripts — so escalating to `SIGKILL`
after the 25s grace does NOT cost the current turn's billing accuracy.
It only forfeits `--resume` for that turn.

**Completion is not guaranteed to arrive on stdout.** Earlier revisions of
this section made teardown conditional on observing the terminal event
there, and left reaping to a plain `cmd.Wait()`. Both assumptions are
unsafe, and together they turn one missing event into an unbounded hang:
nothing signals the process, and the adapter blocks on it for as long as
its caller is willing to wait. On 2026-08-01 a codex Pulse Fixer wrote its
complete result and a native `task_complete` to its rollout, then held its
caller's HTTP response open for 65 minutes at 3.4s of CPU. Every adapter
MUST therefore provide:

1. **An independent completion signal.** Where the CLI records turn state
   on disk, tail it and treat its native terminal marker as authoritative
   — codex's rollout JSONL (`event_msg/task_complete`, matched by
   `session_meta.cwd`) is the reference implementation, shared by the
   interactive and structured adapters. Route every signal through one
   idempotent teardown so the first to fire wins.
2. **A bounded reap.** `procshutdown.Reap(cmd, grace, logger)`, with a
   grace above the full 28s ladder so an in-flight teardown always wins
   the race. A bare `cmd.Wait()` is a contract violation.
3. **Surfaced decode failures.** `scanner.Err()` MUST be checked and MUST
   force teardown. Unchecked, an oversized line is indistinguishable from
   a clean end of stream.

A stalled stream MUST NOT lose a result the CLI already produced: recover
the final assistant text from the transcript before failing the turn.

None of this may curtail a turn that is genuinely still running. Each
trigger fires only on positive evidence that the turn ended — a native
terminal marker, a decode error, or an output stream that has already
closed.

**What the contract is NOT.**

- It is NOT acceptable to send `SIGTERM` and rely on the CLI to exit
  "eventually." Adapters MUST escalate to `SIGKILL` after the third
  SIGTERM's grace expires.
- The escalation MUST NOT be gated on pending in-flight tool calls. By
  the time the terminal event has arrived, any tool calls the CLI is
  still firing are side-effect leakage and MUST be cut off — this is
  the orphan-subprocess hazard.
- It is NOT acceptable to extend the total grace past 25s. A CLI that
  cannot flush state in 25s either has an upstream bug (synchronous
  I/O blocking signal delivery long enough to outlast three SIGTERMs)
  or is doing work it shouldn't be doing after terminal.

**Upstream obligation on each CLI.** Each CLI provider promises: "Given
SIGTERM, I will flush resume state within 25s and exit, OR I accept that
this turn's `--resume` is forfeit." If a CLI cannot honor this (e.g., a
synchronous HTTP call to its MCP bridge with a 120s timeout), the fix
is in that CLI — shorter per-tool timeouts, signal-aware I/O — not in
the adapter's grace budget.

## Required Real E2E Certification Matrix

Every structured/JSON coding provider must have opt-in real E2E tests for each
area below. The test proves the adapter correctly handles the provider's actual
event format end-to-end.

| # | Area | Required proof |
|---|---|---|
| 1 | Fresh launch | Starts the real CLI in structured mode, gets a text response. |
| 2 | Working directory | Provider operates in the exact caller workspace. |
| 3 | System prompt | System instructions reach the provider and a canary appears in the response. |
| 4 | Token usage | Result/finish events produce non-zero input and output token counts. |
| 5 | Streaming text | Text/assistant delta events stream to the channel as content chunks. |
| 6 | Streaming tool calls | Tool call events stream as tool-call-start and tool-call-end chunks. |
| 7 | Session metadata | Session/thread ID captured from events, transport mode in generation info. |
| 8 | Multi-step tool use | Agent uses a tool, gets output, then produces final text. |
| 9 | Model override | Non-default model selector produces a response (adapter passes `--model`). |
| 10 | Image path analysis | Provider reads a local image file path and answers content questions. |
| 11 | Web search | Provider performs web search and returns real internet data. |
| 12 | Live web search | Web search returns data that could not come from model training data. |
| 13 | Cancellation | Context cancellation kills the process group; adapter returns error cleanly. |
| 14 | Error on empty response | Adapter returns a clear error when no text events are received. |
| 15 | MCP bridge tool call | A real MCP bridge tool is callable from the structured transport. |
| 16 | Internal tool disable | Built-in tools disabled via provider flag; agent cannot use shell/file tools. |
| 17 | Multi-turn resume | Second call with resume session ID continues the conversation; agent recalls prior context. |
| 18 | No injected strings | Adapter does not leak internal project names, library names, or custom strings into the prompt. |
| 19 | No internal memory | Fresh session (no resume) cannot recall data from a previous session; agent memory is isolated. |
| 20 | Graceful cancel | Context cancellation mid-run preserves all streamed chunks; stream channel is closed; partial content is returned if available. |
| 21 | Sandboxed MCP | Built-in tools disabled while MCP bridge tools remain callable. Proves the production pattern: agent can only use tools you explicitly provide. |
| 22 | End-of-turn shutdown | On terminal event (result/done/task_complete), adapter calls `procshutdown.Graceful` → 3 SIGTERMs (10s/10s/5s graces) → SIGKILL. Asserts no post-result `tool_use` events leak through after the reap returns. See §9 Process Shutdown Contract. |
| 22a | Completion without stdout | A turn whose terminal event never reaches stdout still tears down and returns within a bounded time: the on-disk transcript marker triggers teardown, `scanner.Err()` forces it, and `procshutdown.Reap` bounds the reap. Asserts the adapter never blocks on a process that outlived its output stream, and that a result already written to the transcript is recovered rather than lost. See §9. |

## Current Test Coverage

### Claude Code (`claude -p --output-format stream-json`)

```sh
RUN_CLAUDE_CODE_PRINT_INTEGRATION=1 go test ./pkg/adapters/claudecode \
  -run 'TestClaudeCodeStructured|TestClaudeCodeStreaming|TestRawClaude' -v -timeout 4m
```

| Area | Test |
|---|---|
| Fresh launch | `TestClaudeCodeStructuredBasicRun` |
| Working directory | `TestClaudeCodeStructuredWorkingDir` |
| System prompt | `TestClaudeCodeStructuredSystemPrompt` |
| Token usage | `TestClaudeCodeStructuredTokenUsage` |
| Streaming text | `TestClaudeCodeStructuredStreaming` |
| Streaming tool calls | `TestClaudeCodeStructuredToolUse` |
| Session metadata | `TestClaudeCodeStructuredSessionMetadata` |
| Multi-step tool use | `TestClaudeCodeStructuredMultiStepToolUse` |
| Model override | `TestClaudeCodeStructuredModelOverride` |
| Image input | `TestClaudeCodePrintRealImageInput` |
| Web search | `TestClaudeCodeRealSearchWeb` |
| Error handling | `TestClaudeCodeStructuredErrorHandling` |
| MCP bridge | `TestClaudeCodeStructuredMCPBridge` |
| Tool disable | `TestClaudeCodeStructuredToolDisable` |
| Multi-turn resume | `TestClaudeCodeStructuredMultiTurnResume` |
| No injected strings | `TestClaudeCodeStructuredNoInjectedStrings` |
| No internal memory | `TestClaudeCodeStructuredNoInternalMemory` |
| Live web search | `TestClaudeCodeStructuredSearchWebLiveData` |
| Graceful cancel | `TestClaudeCodeStructuredGracefulCancel` |
| Sandboxed MCP | `TestClaudeCodeStructuredMCPBridge` (uses `--tools ""` + MCP) |

**Gaps:** none.

### Codex CLI (`codex exec --json`)

```sh
RUN_CODEX_CLI_STREAM_JSON_E2E=1 go test ./pkg/adapters/codexcli \
  -run 'TestCodexCLIRealExecJSON' -v -timeout 6m
```

| Area | Test |
|---|---|
| Fresh launch | `TestCodexCLIStructuredBasicRun` |
| Working directory | `TestCodexCLIStructuredWorkingDir` |
| System prompt | `TestCodexCLIStructuredSystemPrompt` |
| Token usage | `TestCodexCLIStructuredTokenUsage` |
| Streaming text | `TestCodexCLIStructuredStreaming` |
| Streaming tool calls | `TestCodexCLIStructuredToolUse` |
| Session metadata | `TestCodexCLIStructuredSessionMetadata` |
| Multi-step tool use | `TestCodexCLIStructuredMultiStepToolUse` |
| Model override | `TestCodexCLIStructuredModelOverride` |
| Session resume | `TestCodexCLIRealExecJSONContract` (multi-turn with resume) |
| MCP bridge | `TestCodexCLIRealExecJSONMCPBridgeContract` |
| Image input | `TestCodexCLIStructuredImageInput` |
| Web search | `TestCodexCLIStructuredSearchWeb` |
| Live web search | `TestCodexCLIStructuredSearchWebLiveData` |
| Error handling | `TestCodexCLIStructuredErrorHandling` |
| Tool disable | `TestCodexCLIStructuredToolDisable` |
| Multi-turn resume | `TestCodexCLIStructuredMultiTurnResume` |
| No injected strings | `TestCodexCLIStructuredNoInjectedStrings` |
| No internal memory | `TestCodexCLIStructuredNoInternalMemory` |
| Graceful cancel | `TestCodexCLIStructuredGracefulCancel` |
| Sandboxed MCP | `TestCodexCLIRealExecJSONMCPBridgeContract` (uses `--disable shell_tool` + MCP) |

**Gaps:** none.

### Cursor CLI (`cursor-agent --print --output-format stream-json`)

```sh
RUN_CURSOR_CLI_STREAM_JSON_E2E=1 go test ./pkg/adapters/cursorcli \
  -run 'TestCursorCLIStructured' -v -timeout 10m
```

| Area | Test |
|---|---|
| Fresh launch | `TestCursorCLIStructuredBasicRun` |
| Working directory | `TestCursorCLIStructuredWorkingDir` |
| System prompt | `TestCursorCLIStructuredSystemPrompt` |
| Token usage | `TestCursorCLIStructuredTokenUsage` |
| Streaming text | `TestCursorCLIStructuredStreaming` |
| Streaming tool calls | `TestCursorCLIStructuredToolUse` |
| Session metadata | `TestCursorCLIStructuredSessionMetadata` |
| Multi-step tool use | `TestCursorCLIStructuredToolUse` |
| Model override | `TestCursorCLIStructuredModelOverride` |
| Image path | `TestCursorCLIStructuredImagePath` |
| Web search | `TestCursorCLIStructuredSearchWeb` |
| Live web search | `TestCursorCLIStructuredSearchWebLiveData` |
| Error handling | `TestCursorCLIStructuredErrorHandling` |
| MCP bridge | `TestCursorCLIStructuredMCPBridge` |
| Tool disable | `TestCursorCLIStructuredToolDisable` |
| Multi-turn resume | `TestCursorCLIStructuredMultiTurnResume` |
| No injected strings | `TestCursorCLIStructuredNoInjectedStrings` |
| No internal memory | `TestCursorCLIStructuredNoInternalMemory` |
| Sandboxed MCP | `TestCursorCLIStructuredSandboxedMCP` (uses `--mode ask` + `--approve-mcps`) |
| Graceful cancel | `TestCursorCLIStructuredGracefulCancel` |

**Gaps:** none.

## Full Provider Coverage Matrix

| Area | Claude | Codex | Cursor |
|---|:---:|:---:|:---:|
| 1. Fresh launch | yes | yes | yes |
| 2. Working directory | yes | yes | yes |
| 3. System prompt | yes | yes | yes |
| 4. Token usage | yes | yes | yes |
| 5. Streaming text | yes | yes | yes |
| 6. Streaming tool calls | yes | yes | yes |
| 7. Session metadata | yes | yes | yes |
| 8. Multi-step tool use | yes | yes | yes |
| 9. Model override | yes | yes | yes |
| 10. Image path | yes | yes | yes |
| 11. Web search | yes | yes | yes |
| 12. Live web search | yes | yes | yes |
| 13. Cancellation | yes | yes | yes |
| 14. Error handling | yes | yes | yes |
| 15. MCP bridge | yes | yes | yes |
| 16. Tool disable | yes | yes | yes |
| 17. Multi-turn resume | yes | yes | yes |
| 18. No injected strings | yes | yes | yes |
| 19. No internal memory | yes | yes | yes |
| 20. Graceful cancel | yes | yes | yes |
| 21. Sandboxed MCP | yes | yes | yes |

## Related Docs

- `docs/coding_sdk_tmux_contract.md`
- `docs/CODING_AGENT_TRANSPORT_PATTERNS.md`
