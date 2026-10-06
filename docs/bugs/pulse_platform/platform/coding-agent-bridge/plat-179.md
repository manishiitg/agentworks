[← platform / coding-agent-bridge](index.md)

# PLAT-179 — a retained (tmux-delivered) coding-agent turn is declared complete on the first non-empty assistant text, even explicit intermediate commentary, before a queued tool call runs

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P0 |
| Product | platform |
| Area | coding-agent-bridge |
| Summary | P0 — silently returns the wrong answer and ends the turn |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `fixed` — root cause confirmed live, fix confirmed live end-to-end for pi-cli, build/test verified |
| Last synchronized | `2026-08-22` |

- **Priority:** P0 — silently returns the wrong answer and ends the turn
  early, for every tmux-backed coding-agent provider using retained/live-steer
  delivery (claude-code, codex-cli, cursor-cli, agy-cli, pi-cli), not one
  provider. A caller has no way to tell a genuinely-final reply from a
  truncated one without independently re-verifying.
- **Owner:** `mcpagent/agent/retainedturn/retained_turn.go` (the actual fix);
  `multi-llm-provider-go/pkg/adapters/picli/picli_transcript.go` (the paired
  change pi-cli needed to feed that fix real data). claude-code and cursor-cli
  need the same transcript-preservation audit — see Related Work.
- **Related:** [PLAT-174](../../goals/step-execution/plat-174.md)), [PLAT-175](../../goals/step-execution/plat-175.md)),
  [PLAT-176](../scheduler-runs/plat-176.md)) (same investigation session — started as a request
  to validate pi-cli against OpenRouter, surfaced this along the way).
  [PLAT-180](plat-180.md) (a second, separate defect surfaced once this one
  was actually fixed and the test could run further).

## How this surfaced

Testing whether `pi-cli` works correctly with an OpenRouter model
(`stealth/ox-alpha`), via `mcp-agent test coding-agent-chat-e2e
--provider pi-cli --retained-window-p0-only`, which exercises the IC-11
cross-repository retained-session contract.

The investigation took three wrong turns before finding the real cause,
recorded here so a future reader doesn't retrace them:

1. First hypothesis: OpenRouter-specific. Disproven — identical failure with
   the catalogued Gemini baseline model.
2. Second hypothesis: pi-cli chat uses "structured" (no-pane) transport, so
   the tmux-liveness check in IC-11 doesn't apply to it. Disproven —
   `[QUERY->LIVE] ... provider=pi-cli transport=tmux` in the server log is
   computed by mcpagent's own delivery code; pi-cli chat sessions genuinely
   use tmux, same as the other three providers.
3. Third hypothesis (after #2): pi-cli's tmux session isn't registering with
   `/api/terminals`, a real gap in event-driven terminal-store population.
   Disproven — `/api/terminals` was returning a deliberate `http.NotFound`
   from `runtimeDiagnosticsHandler` because `AGENTWORKS_RUNTIME_DEBUG` wasn't
   set on the local dev server. Not a bug; a missing local env var. Setting it
   made `assertRetainedTmuxLive` pass immediately.

With that noise cleared, the real failure was one step further in:

```
Error: IC-11 retained-window P0 failed: retained turn completed before
execute_shell_command started; intermediate commentary was treated as final
```

The IC-11 prompt asks for three things in order: (1) a brief progress update,
explicitly labeled *"intermediate commentary, not your final answer"*, (2)
one `execute_shell_command` call running `sleep 2; printf <token>`, (3) only
then the real final answer. The turn reported `"status":"completed"` after
**2.2 seconds** — not enough time for even the `sleep 2` to finish — with
`final_result` set to the progress-update text, not the real answer.

## Root cause

Traced through both repositories:

- `mcpagent/agent/turn_session.go`'s `startRetainedCompletionWatch` polls
  every 100ms and calls a reader function. The moment that reader returns any
  non-empty string, it calls `completeRetainedTurn` and emits a
  `canonical_turn_completion` event — unconditionally:
  ```go
  case <-ticker.C:
      finalResult := strings.TrimSpace(reader(provider, s.agent.sessionID, startedAt))
      if finalResult == "" {
          continue
      }
      s.completeRetainedTurn(lifecycle, seq, input, finalResult, provider, transport, startedAt)
      return
  ```
  This loop's own contract — "empty means not done yet, keep polling" — is
  sound.

- The reader is `mcpagent/agent/retainedturn.FinalResponse` →
  `finalResponse()`, which reads the provider's reconstructed messages (via
  `ReadCodingAgentRetainedTurnMessages`) and picks the last AI-role message
  with any non-empty text. Coding-agent providers can legitimately bundle
  intermediate commentary and the tool call it introduces into ONE assistant
  message — confirmed live for pi-cli:
  ```
  {"role":"assistant","content":[{"type":"text","text":"<progress update>"},
                                   {"type":"toolCall","id":"call_...",...}]}
  ```
  and true by construction for claude-code and cursor-cli too, which group a
  single LLM call's content blocks into one `MessageContent` the same way.
  `finalResponse()` only looked at the text; a message that also carried a
  pending tool call was indistinguishable from a genuinely finished reply.

## First fix attempt — reverted, recorded so it isn't retried

The first fix gated `ReadRetainedTurnMessages` (pi-cli only) on the coding
CLI's own tmux pane reporting "idle" text, reusing `piPaneReadyForInput` — the
same check the ordinary non-retained turn flow already trusts
(`picli_interactive_adapter.go:1250`).

**This failed live, in the opposite direction from the original bug.** Watched
the actual tmux pane in real time while pi genuinely finished — the tool call
had succeeded, the correct final answer was sitting right there on screen —
and captured the full pane text: it contained **zero occurrences of the word
"idle"** anywhere, in this pi CLI build's status line format. The gate never
fired. The retained turn hung for the full 5-minute test timeout instead of
completing — worse than the bug it replaced, which at least returned promptly
with the wrong text. `git commit b71d825` (`multi-llm-provider-go`) shipped
this; `commit c4ae920` reverted it once the real cause was found.

The deeper lesson: pane-text matching is provider-UI-format-fragile and was
never how the two *already-correct* providers solved this same problem —
codex-cli filters on `phase=="final_answer"` and claude-code has an (unwired)
`stop_reason=="end_turn"` check — both transcript-level facts, neither
touches a pane. The real fix needed no pane inspection either.

## Fix

`finalResponse()` (`mcpagent/agent/retainedturn/retained_turn.go`) now treats
the newest AI-role message as authoritative. If that message also carries a
`ToolCall` part, the turn is still in progress and the reader returns empty:

```go
if messageHasToolCall(messages[i]) {
    return ""
}
```

It must not `continue` scanning older AI messages: an older commentary-only
message may exist, and returning it would reproduce the premature completion
under a different transcript shape.

This is a **shared, single-point fix** — every retained/live-steer provider's
reconstruction bundles text+toolCall into one message the same way, so this
correctly protects claude-code and cursor-cli too, not just pi-cli, without
duplicating a heuristic per provider.

It needed one paired change to actually take effect for pi-cli:
`multi-llm-provider-go/pkg/adapters/picli/picli_transcript.go`'s
`piTranscriptText` extracted only `"text"`-typed content blocks, silently
discarding a `"toolCall"` block in the same message — so by the time a
message reached `finalResponse()`, the very fact this fix needed to see was
already gone. Replaced with `piTranscriptParts`, which preserves a `toolCall`
block as a real `llmtypes.ToolCall` (ID + function name only — nothing
downstream needs to replay the call) alongside any text, in the order they
appeared in the raw content array.

## Related work not done in this pass

- **claude-code and cursor-cli's own transcript readers were not audited** for
  the same "does it preserve a ToolCall part in the same message" question
  pi-cli needed fixed. The shared `finalResponse()` fix protects them only if
  their readers actually hand it a `ToolCall` part when one exists — claude-code's
  `readClaudeTranscriptMessages` (used by `ReadRetainedTurnMessages`) appears
  to via `assistantBlocksToParts` based on a read of the code, but this was
  not live-verified for either provider, only for pi-cli.
- **A chat-mode E2E artifact for "structured" transport was written, then
  reverted.** While investigating hypothesis #2, a new
  `--structured-resume-p0-only` test mode was added to
  `cmd/testing/coding_agent_chat_e2e.go` (mcp-agent-builder-go), asserting
  `DeliveryTransport == "structured"`. Once #2 was disproven — chat sessions
  use `"tmux"` transport, not `"structured"` — that assertion could never
  legitimately pass through this harness (structured transport is a
  workflow-step-only path, `MetadataKeyStructuredTransport`, never reachable
  via `/api/query`). Reverted rather than left in the tree as a misleading,
  permanently-failing artifact.
- **A temporary, env-gated test bypass** (`AGENTWORKS_CURSOR_FORCE_TMUX_TEST`,
  `agent_go/cmd/server/coding_agent_modes.go`) was used to force cursor-cli
  through the tmux-retained path — which it never takes in real product
  usage (`codingAgentUsesStructuredTransport` deliberately routes it to
  structured transport) — specifically to reproduce and confirm this same bug
  class for cursor-cli. Reverted after use; not a product change.

## Verification

- `mcpagent/agent/retainedturn/retained_turn_test.go`:
  `TestFinalResponseSkipsAnAIMessageThatAlsoHasAToolCall` and
  `TestFinalResponseReturnsEmptyWhenOnlyMessageHasAPendingToolCall`. The
  latter is the real regression catcher — confirmed failing before the fix
  (returned the commentary text instead of empty) and passing after.
- `TestFinalResponseReturnsEmptyWhenNewestAssistantMessageHasToolCall` proves
  older progress text is not mistaken for the final answer while the newest
  assistant message has a pending tool call; a paired test proves text after
  that tool call completes is returned normally.
- `multi-llm-provider-go/pkg/adapters/picli/picli_transcript_test.go`:
  `TestPiTranscriptPartsPreservesAToolCallAlongsideText` and
  `TestPiTranscriptPartsTextOnlyMessageHasNoToolCall`. Confirmed failing
  before (`piTranscriptParts` undefined) and passing after.
- Full `pkg/adapters/picli` suite passes (includes real-tmux tests).
  `go build ./...` clean in both repositories.
- **Confirmed live end-to-end for pi-cli**: watched the retained turn via
  direct tmux pane capture — genuine progress commentary, tool call, then the
  real final answer — and confirmed via the E2E harness that the turn no
  longer completes on the commentary. (It now correctly proceeds past this
  check and hits a second, separate, already-filed defect —
  [PLAT-180](plat-180.md) — confirming this fix works: the test could not
  have reached that later check while this one was still broken.)

## Acceptance

- [x] A retained pi-cli turn does not complete on intermediate commentary
      alone; it waits for the actual final (non-tool-call) message.
- [x] Live: confirmed pi-cli no longer returns the progress-update text as
      `final_result`.
- [ ] claude-code and cursor-cli's transcript readers audited to confirm they
      hand `finalResponse()` a `ToolCall` part when one exists, matching what
      pi-cli needed.
