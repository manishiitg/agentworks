# Costs and Conversation History

How token/cost accounting and turn-by-turn conversation logs flow from an
LLM adapter through `mcpagent` to the consumer (e.g. `mcp-agent-builder-go`).

This doc covers both planes because they share a pipeline shape and a
small number of orthogonal artifacts. If you're touching either, read both
— the per-tmux-provider quirks (Claude/Codex/Cursor) affect both.

The layering, top-down:

```
mcp-agent-builder-go  ← persists, serves /api/cost/summary, writes chat logs
        │
   mcpagent           ← runs the agent loop, maintains conversation_history
        │
multi-llm-provider-go ← adapters: produce ContentResponse + GenerationInfo
```

There is no direct call from `mcp-agent-builder-go` into an adapter.
Everything bubbles up through `mcpagent`.

---

## Files written per turn

```
<workflow-phase>/                              # workflow-phase chats
├── builder/conversation/<YYYY-MM-DD>/
│   └── session-<sid>-conversation.json        # conversation_history (full turn trail)
├── costs/phase/token_usage.json               # per-phase rollup, per-model
└── costs/phase/daily/<YYYY-MM-DD>.json        # per-day rollup, per-model

workspace-docs/_users/<userID>/
└── chat_history/<sid>/conversation.json       # regular (non-workflow) chats

_system/
└── costs.jsonl                                # global append-only ledger (one event per line)
```

All four paths receive data from the **same** in-memory pipeline. The
choice between the workflow-phase log and the per-user chat log is made
by `mcp-agent-builder-go` based on whether `req.WorkflowPhase` is set;
the ledger and rollups are written unconditionally.

---

## Cost tracking

### Pipeline

```
adapter.GenerateContent
  → ContentResponse.GenerationInfo {PromptTokens, CompletionTokens, ...,
                                    Additional[cost_usd_estimated, cost_model_id]}
  → mcpagent emits TokenUsageEvent (turn-level)
  → mcp-agent-builder-go costObserver.HandleEvent
  → ledger.Append            → writes one line to _system/costs.jsonl
  → ApplyModelUsageToPhase   → updates token_usage.json + daily rollup
```

### Where each provider's tokens come from

| Provider              | Source of truth                        | Notes |
|-----------------------|-----------------------------------------|-------|
| Direct-API (Anthropic, OpenAI, …) | provider response usage    | Adapter reads native usage on the response. |
| Claude Code (tmux)    | `~/.claude/projects/<dir>/<sid>.jsonl` | Sidecar JSONL parsed by `readClaudeTranscriptUsage`. |
| Codex CLI (tmux)      | `~/.codex/sessions/.../rollout-*.jsonl` | Sidecar JSONL parsed by `readCodexTranscriptUsage`. Selected by `mtime ≥ turnStart−30s` AND cwd match. |
| Cursor CLI (tmux)     | **char-based estimate**                 | Cursor is subscription-priced and does NOT expose per-turn token data anywhere we can read. The adapter falls back to `(chars + 3) / 4` for both prompt and completion. Expect ±20–30% off true tokenizer counts. |

For tmux providers, the adapter populates `GenerationInfo.PromptTokens /
CompletionTokens / CachedContentTokens / ReasoningTokens` from the
sidecar, then computes `cost_usd_estimated` via
`ComputeUSDCostFromMetadata` using `cost_model_id`. The consumer routes
these into `costs.jsonl` and the rollups.

### Claude Code dedup (bug fix worth remembering)

claude-code writes **one JSONL row per content block** (text chunk OR
tool_use). All rows from the same LLM call share `message.id`. Each row
also carries the call's *cumulative* total usage — repeated on every
row.

The early parser summed usage across all rows, inflating cost ~5–10×
on tool-use loops. The fix (`claudecode_transcript_usage.go`) dedupes
by `message.id` (falls back to `requestId`), so each call's usage is
counted exactly once. See the unit test
`TestReadClaudeTranscriptUsageDedupesByMessageID` for the canonical
fixture.

If you see Claude cost looking 2× higher than expected, the first
hypothesis should be: someone re-introduced summing across blocks.

### Cache token surfacing contract

**Every adapter that exposes cache tokens MUST also write the raw
Anthropic-style key into `gi.Additional`**, not just the typed
`gi.CachedContentTokens` field.

Why: the cost ledger pipeline in `mcp-agent-builder-go`
(`extractCacheTokens` in `cost_routes.go`) reads `tu.GenerationInfo`,
which is the **Additional map only** — the typed `*GenerationInfo`
struct doesn't ride on `TokenUsageEvent`. An adapter that writes
only `gi.CachedContentTokens` will land ledger entries with
`cache_read_tokens=0` even when the provider served most of the
prompt from cache.

Required keys, regardless of provider:

| Key | Meaning | Set when |
|---|---|---|
| `cache_read_input_tokens` | Tokens served from prompt cache (cheap) | Provider reports any cache hit |
| `cache_creation_input_tokens` | Tokens billed at creation rate | Anthropic-only (the rest don't have a separate write event) |

Adapter idiom (see `claudecode_transcript_usage.go:115-128` or
`codexcli_transcript_usage.go:165-175` for canonical examples):

```go
if cacheRead > 0 {
    gi.CachedContentTokens = intRef(cacheRead)        // typed
    if gi.Additional == nil {
        gi.Additional = map[string]interface{}{}
    }
    gi.Additional["cache_read_input_tokens"] = cacheRead   // raw
}
if cacheCreate > 0 { // Anthropic-style providers only
    gi.Additional["cache_creation_input_tokens"] = cacheCreate
}
```

For tmux adapters that get cache numbers from a sidecar parser, the
adapter must also **merge** `usage.Additional` into its local
`additional` map (the parser's keys won't reach the consumer
otherwise). See `codexcli_interactive_adapter.go:250-263` for the
merge pattern.

Aliases like `cached_tokens` / `CacheReadInputTokens` (PascalCase)
are accepted for callers that already consume them, but the
lowercase Anthropic-style names are the **authoritative ledger
contract**. New adapters should write the lowercase names; legacy
adapters keep both for backward compatibility.

Cache-key audit (status at the time of writing):

| Provider | Transport | `cache_read_input_tokens` | `cache_creation_input_tokens` |
|---|---|---|---|
| Anthropic | API | ✅ | ✅ |
| OpenAI / OpenRouter | API | ✅ | N/A — caching is read-only |
| Claude Code | structured (`-p`) | ✅ | ✅ |
| Claude Code | tmux | ✅ | ✅ |
| Codex CLI | structured (`exec --json`) | ✅ | N/A |
| Codex CLI | tmux | ✅ | N/A |
| Cursor CLI | structured | ✅ | N/A |
| Cursor CLI | tmux | char-estimated, no cache data exposed | N/A |

### Reasoning token surfacing contract

Parallel to the cache contract above, but with a key asymmetry:

- **Authoritative path**: `gi.ReasoningTokens` (typed `*int` on
  `GenerationInfo`). `mcpagent`'s `extractAllTokenTypes(resp)` reads
  this and accumulates into `cumulativeReasoningTokens`; the
  resulting `TokenUsageEvent.ReasoningTokens` (a typed event field,
  not part of Additional) is what the cost ledger's `Entry`
  reads. **This is what makes reasoning tokens show up in the
  ledger.**
- **Belt-and-suspenders**: also set
  `gi.Additional["reasoning_tokens"]` to the same value. The cost
  ledger pipeline doesn't currently read this key, but downstream
  sinks that forward `gi.Additional` verbatim (Langfuse,
  inspector debug, observability traces) consume it under this
  canonical name.
- **Provider-specific diagnostics** (optional): mirror under a
  prefixed key like `claude_extended_thinking_tokens` for per-provider analysis
  without conflicting with the canonical pair.

### mcpagent's accumulateTokenUsage field-name check

`mcpagent/agent/agent.go:accumulateTokenUsage` gates token
accumulation on whether the response looks like real usage (not an
estimation). The check used to only inspect `gi.InputTokens` /
`gi.OutputTokens` (legacy naming). Adapters that populate the
modern `gi.PromptTokens` / `gi.CompletionTokens` (Claude Code tmux,
codex, gemini transcript readers) without also
populating the legacy pair OR setting `resp.Usage` fell through
to the "skip estimated" branch — and the cumulative counters
that drive `TokenUsageEvent.PromptTokens/CompletionTokens` stayed
at zero, so the cost ledger received zero-token entries.

The check now accepts either pair as evidence of real usage. The
single-turn cost HTTP e2es did not catch this because they bypass
mcpagent and build the `TokenUsageEvent` themselves from
`gi.PromptTokens` directly. The **comprehensive multi-turn e2e**
(see `cmd/server/multi_turn_chat_e2e_real_test.go` in
mcp-agent-builder-go) drives through mcpagent so it does cover this
path — that's where this regression would land.

If you ever see cost ledger entries with no `prompt_tokens` /
`completion_tokens` fields but the adapter logs show real
`input_tokens=N` numbers, this is the failure mode to check first.

### Claude Code CLI upgrade-notice false-busy

When the Claude Code CLI has a new release available, the TUI
parks a `current: X · latest: Y` line at the very bottom of the
pane. `hasReadyInputPrompt` scans from the last line backward
looking for `❯` and rejected as "not ready" when it hit this
line first — so the wait loop polled the same idle pane forever
and the adapter never returned (terminal view showed Claude done,
chat tree never showed completion, no cost ledger entry was
written).

`isIgnorableClaudePromptFooterLine` now also skips lines containing
both "current:" and "latest:". If Claude CLI ever ships a new
footer-style notice, replicate this pattern. Regression fixture
is `TestHasReadyInputPromptAcceptsIdlePromptWithUpgradeNotice`.

### Cursor pricing gotcha

The cursor structured adapter emits `cost_usd_estimated` only when the
effective model id is in the metadata registry. The cursor CLI surfaces
a **display name** (e.g. `"Composer 2.5"`) rather than the registry
id (e.g. `"composer-2-fast"`), so the lookup returns nil and no cost
is emitted. Tracked as a known gap in
`cost_http_e2e_cursor_real_test.go`.

---

## Conversation history

### What gets persisted

`conversation_history` is `[]llmtypes.MessageContent` after
`cleanChatHistoryForPersistence` (strips hidden prompt context the
frontend mustn't see). Same shape in both the workflow-phase log and
the per-user chat log:

```json
{
  "session_id": "...",
  "phase_id": "...",                         // workflow only
  "conversation_history": [
    {"Role": "human", "Parts": [{"Text": "..."}]},
    {"Role": "ai",    "Parts": [{"Text": "..."}]},
    {"Role": "ai",    "Parts": [{"ID":"...", "FunctionCall":{"Name":"...", "Arguments":"..."}}]},
    {"Role": "tool",  "Parts": [{"ToolCallID":"...", "Content":"..."}]}
  ],
  "runtime": {...},
  "ui_events": [...],
  "updated_at": "RFC3339"
}
```

`MessageContent.Parts` is `[]ContentPart`, an interface, with concrete
types: `TextContent`, `ToolCall`, `ToolCallResponse`, `ImageContent`,
`DocumentContent`. JSON serialization is one-way — the concrete type
of each Part isn't tagged, so reading back into typed `[]ContentPart`
isn't lossless. Treat the on-disk JSON as a frozen audit log, not a
roundtrip format.

### How the loop is captured for tmux providers

For tmux coding agents (Claude Code / Codex / Cursor / Pi CLIs),
the CLI runs its **internal** tool-use loop hidden behind the adapter
boundary: only the final assistant text crosses back via
`ContentResponse.Choices[0].Content`. Without help, the persisted
`conversation_history` would only show `[human, ai_final]` — the
intermediate Read/Grep/Edit work would be invisible.

The fix is a transport-agnostic capability on `GenerationInfo`:

```go
type CodingProviderIntermediateMessages struct {
    Provider  string
    Transport string                 // "tmux" | "structured" | "api"
    Messages  []MessageContent
}
```

Defined in `llmtypes/coding_provider_intermediate_messages.go`,
mirroring the existing `CodingProviderSessionHandle` pattern.
`AttachCodingProviderIntermediateMessages` writes to both the typed
field and `Additional[...]` for callers that forward Additional verbatim.

Each tmux adapter reconstructs its turn's trail from the local sidecar
and attaches the result. `mcpagent` extracts and splices into its
in-memory conversation_history before the final assistant text, at
`agent/conversation.go` in the "no tool calls / return final" branch:

```go
if intermediate, ok := llmtypes.ExtractCodingProviderIntermediateMessages(
        choice.GenerationInfo); ok {
    messages = append(messages, intermediate.Messages...)
}
// then the existing final-text append
```

The persistence side (`mcp-agent-builder-go`) needs **no changes** —
the splice happens up the call chain, so the conversation_history that
gets written to disk already includes the intermediate trail.

### Per-tmux-provider sidecar parsers

#### Claude Code — `claudecode_transcript_messages.go`

- File: `~/.claude/projects/<dir>/<sid>.jsonl`
- One JSONL row per content block. Rows of the same call share `message.id`
  but carry *different* single-element `content[]` payloads (e.g. one
  row for `text`, one for `tool_use`).
- The parser **groups by `message.id`** and accumulates blocks across
  rows into one `MessageContent` per LLM call — matching the shape the
  Anthropic Messages API would have returned.
- Filters by `turnStart` so multi-turn sessions don't leak prior turns.
- `thinking` / `redacted-reasoning` blocks are silently skipped.

#### Codex CLI — `codexcli_transcript_messages.go`

- File: `~/.codex/sessions/YYYY/MM/DD/rollout-<ts>-<uuid>.jsonl`
- Per-event format, mixed types: `response_item:message`,
  `response_item:function_call`, `response_item:function_call_output`,
  `response_item:custom_tool_call` (apply_patch etc.),
  `response_item:custom_tool_call_output`, plus telemetry-only
  `event_msg:*` rows we ignore.
- File selection: walk `~/.codex/sessions/`, keep `mtime ≥
  turnStart−30s`, sort by mtime desc, take the first whose
  `session_meta.cwd` matches. **Recency alone is unsafe** — a parallel
  Codex Desktop or VS Code Codex session would tail-grab the wrong
  rollout.
- `response_item:reasoning` (encrypted), `role=developer|user`, and
  prior-turn rows are skipped.

#### Cursor CLI — `cursorcli_transcript_messages.go`

- File: `~/.cursor/chats/<md5(absolute_cwd)>/<agentId>/store.db`
  (sqlite, not JSONL).
- Schema: `meta(key,value)` with one row whose `value` is **hex-encoded
  JSON** containing `latestRootBlobId`; `blobs(id, data)` with
  content-addressed SHA256 blobs (JSON message blobs + protobuf
  wrappers).
- The root protobuf has field-1 length-delimited 32-byte refs to
  message blobs in chronological order, plus metadata fields the parser
  ignores. We walk those refs and JSON-decode each.
- Driver: `modernc.org/sqlite` (pure-Go; consistent with the
  consumer's existing dep).
- Skipped: `role:"system"`, the first `role:"user"` (provider-options
  context payload — cwd / mcp / rules), and `redacted-reasoning`.
- **Async-commit gotcha**: cursor commits the root blob to sqlite up
  to ~20s AFTER the tmux pane settles (observed on a trivial "Reply OK"
  turn). The parser retries for ~4s. Fast trivial turns may still
  return empty; real tool-using turns produce mid-flow commits and are
  usually captured.
- **Multi-turn duplication gotcha**: cursor's root is *cumulative* —
  it references all messages from session start through the latest
  turn, with no per-message timestamps. The current parser returns the
  whole root each turn, so a multi-turn cursor chat will see turn 1's
  messages re-spliced on turn 2. Fix when needed: cache the prior
  turn's root-ref-set in the adapter and return only the diff. Not
  shipped because workflow phases are typically one-shot.

## Adding a new tmux provider

1. Implement `read<Provider>TranscriptUsage(turnStart, ...)` that pulls
   token + model + native-session-id from the CLI's local sidecar.
   Return zero values when uncertain — best-effort, no errors.
2. Implement `read<Provider>TranscriptMessages(turnStart, ...)` that
   maps the sidecar's content events to `[]llmtypes.MessageContent`.
   Skip system / outer-user / encrypted-reasoning rows.
3. In the adapter's `GenerateContent`, after building
   `GenerationInfo`:
   ```go
   llmtypes.AttachCodingProviderSessionHandle(gi, ...)
   if msgs := read<Provider>TranscriptMessages(...); len(msgs) > 0 {
       llmtypes.AttachCodingProviderIntermediateMessages(gi,
           llmtypes.CodingProviderIntermediateMessages{
               Provider:  "<provider-name>",
               Transport: llmtypes.CodingProviderTransportTmux,
               Messages:  msgs,
           })
   }
   ```
4. Add a unit test for the parser with a synthetic sidecar fixture.
5. Optionally add a live cost HTTP E2E (see
   `cost_http_e2e_*_real_test.go` patterns in `mcp-agent-builder-go`)
   to verify the full flow through the ledger.

No `mcpagent` or `mcp-agent-builder-go` changes are needed — the
splice and persistence machinery is transport-agnostic.

---

## Tests to read for the canonical fixtures

- `pkg/adapters/claudecode/claudecode_transcript_usage_test.go`
  — token dedup by `message.id`.
- `pkg/adapters/claudecode/claudecode_transcript_messages_test.go`
  — block accumulation across rows of the same call.
- `pkg/adapters/codexcli/codexcli_transcript_messages_test.go`
  — event-type mapping + cwd scoping.
- `pkg/adapters/cursorcli/cursorcli_transcript_messages_test.go`
  — synthesized sqlite store.db + protobuf root walk.
- `mcp-agent-builder-go/agent_go/cmd/server/cost_http_e2e_*_real_test.go`
  — live end-to-end through the ledger (each provider gated on its own
  env var; consult the file).
