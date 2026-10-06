# PLAT-103 — retained turns complete without their structured final response

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P0 |
| Product | coding-agents |
| Area | bridge |
| Summary | AgentWorks correctly delivered a follow-up directly to a retained coding CLI, |

| Field | Value |
|---|---|
| Status | `fix implemented; runtime reverify pending` — the 2026-08-19 Claude premature-completion regression is now guarded by a captured-transcript test; a restarted backend must still prove the real Sales Outreach path |
| Priority | P0 |
| Owner | coding-agent retained-turn output contract |
| Reported | 2026-08-14 |
| Related | [PLAT-020](plat-020.md), [PLAT-035](plat-035.md), [PLAT-102](plat-102.md) |

## Problem

AgentWorks correctly delivered a follow-up directly to a retained coding CLI,
and Raw terminal mode showed the completed assistant answer. Formatted mode did
not show it. The backend's retained-turn watcher emitted a synthetic
`unified_completion` containing lifecycle metadata only; it omitted
`final_result`. Successful empty completions are intentionally not rendered as
assistant messages, so the UI could remain on a spinner or show no reply.

## Reopened — Sales Outreach live reproduction (2026-08-19)

The same user-visible failure recurred in the Sales Outreach Chat session
`c2b1bb4c-5a4d-4fd1-9709-ad7329c34a7b`, backed by Claude Code native session
`87de1833-360c-4df8-9bfb-db7d10b014ad`.

The evidence pins a premature completion rather than a frontend repaint issue:

- `11:15:40` IST — the retained message was accepted;
- `11:15:45` — Claude emitted the interim text “Let me check what sequence the
  workflow actually defines.” with `stop_reason=tool_use`;
- `11:15:47` — AgentWorks consumed `source=mcpagent_session`
  `unified_completion` and marked the retained turn completed;
- Claude continued executing tools through `11:15:56`;
- `11:16:10` — Claude committed the real answer with
  `stop_reason=end_turn`, visible in tmux but absent from Formatted mode.

The exact wiring defect is now confirmed. `mcpagent/agent/retainedturn` asks
`ReadCodingAgentRetainedTurnMessages` for a final response. Claude's adapter
implements that call with `readClaudeTranscriptMessages`, which intentionally
returns the entire in-progress tool loop. The selector then chooses the latest
textual assistant block without checking its stop reason. Claude already has
`completedAssistantResponseFromTranscript`, which accepts only a committed
`stop_reason=end_turn`, but the retained Session completion path does not use
it.

This runtime evidence invalidates the prior `implemented; runtime reverify
pending` status. The repair must return no retained final response while Claude
has only `tool_use` messages, then emit exactly one non-empty completion after
the matching `end_turn` is committed.

### Implemented repair

Claude's `ReadRetainedTurnMessages` now calls the existing
`completedAssistantResponseFromTranscript` oracle. It returns no messages while
the transcript contains only `tool_use`, and returns one AI message only after
Claude commits a non-empty `end_turn` response. The generic tool-trail reader
remains available for observability but no longer decides retained completion.

`TestReadRetainedTurnMessagesWaitsForEndTurn` uses the captured production
shape: interim text plus multiple tool rows first, followed later by the
thinking and text rows for one `end_turn` message. It proves the first read is
empty and the completed read contains only the real final answer. The complete
Claude adapter package, AgentWorks retained-settlement tests, and the retained
P0 assertion tests pass. Runtime re-verification still requires rebuilding and
restarting AgentWorks because its Go module replaces the provider dependency
with this local checkout at build time.

## Root cause

The normal `mcpagent` turn path already reconstructs the coding CLI's assistant
and tool trail from the provider's authoritative sidecar transcript. Direct
retained input bypasses that turn wrapper for speed. Its completion path reused
the tmux idle-composer boundary but never invoked the existing sidecar readers.

Tmux correctly answered **when the turn ended** but was incorrectly treated as
if it also supplied the structured response. Scraping the visible pane is not a
safe repair because wrapping, repainting, tool progress, and terminal chrome can
corrupt the text.

## Implemented repair

- Each supported tmux coding provider now exposes a read-only retained-turn
  sidecar lookup: Codex rollout JSONL, Claude Code transcript JSONL, Cursor
  `store.db`, and Pi transcript JSONL.
- `mcpagent/agent/retainedturn` normalizes those messages and selects the last
  textual assistant response without growing the deliberately minimized core
  `mcpagent.Agent` API.
- AgentWorks retains its existing fast direct-tmux send and stable-idle
  completion detector.
- At that completion boundary it reads the sidecar from the recorded turn start
  and emits a typed `UnifiedCompletionEvent` with the real `final_result`,
  duration, provider, tmux session, and main-agent scope.
- An empty sidecar remains visible in backend diagnostics through
  `final_response_missing`; it is never silently replaced by pane scraping.

## Verification completed

- Regression coverage proves the retained completion reader's result is stored
  as `unified_completion.final_result` with source `coding_agent_sidecar`.
- Existing retained live-input coverage still passes, proving delivery was not
  replaced with full agent construction.
- The focused Codex, Claude Code, Cursor, and Pi adapter suites pass.
- `mcpagent/agent` and `mcpagent/agent/retainedturn` tests pass, including the
  pinned 45-function core API inventory.
- `go build ./...` passes in AgentWorks.
- The complete provider suite had one unrelated pre-existing/flaky tmux sandbox
  failure (`TestPreparedSandboxExecutesInsideTmuxPane`); every affected provider
  adapter package passed.

## Runtime acceptance

The first live verification exposed a presentation regression adjacent to the
retained-response repair: ordinary wrapped turns already carry their answer in
both `llm_generation_end` and `unified_completion`. The formatted transcript
deduplicated long repeated answers but deliberately kept matches shorter than
24 characters, so `Hi! Ready when you are.` appeared twice. Exact normalized
matches are now collapsed at every length; the length guard remains only for
the less-certain containment comparison. Regression tests cover both the short
exact match and two distinct short answers.

A second live verification exposed the symmetric user-message race. The
live-input endpoint records its durable `user_message` before returning HTTP;
SSE can therefore insert that event while the frontend is awaiting the
acknowledgement. The frontend previously appended its optimistic bubble
unconditionally afterward, while its existing dedupe handled only the opposite
arrival order. Optimistic insertion now checks the acknowledgement's stable
`message_id` against events already received. This removes the duplicate in
both arrival orders without collapsing a later intentional repeat of identical
text. Focused frontend regression coverage passes.

A third live verification exposed two lifecycle holes after a retained message
was accepted in 97 ms. Codex recorded the user message in its own rollout but
did not produce a final assistant response. The tmux watcher inspected the pane
once while it was still busy and then stopped checking unless another decoded
output chunk arrived. If the tmux control stream later closed, the watcher also
returned silently. Together those paths left Formatted mode on `Working`
forever even after the provider process disappeared.

The watcher now rechecks a non-ready pane on a bounded interval, independent of
future repaint notifications. A closed control stream or failed pane capture is
also reconciled: a durable sidecar answer completes the logical turn, a
still-live provider pane gets a fresh observer, and a missing/dead provider
process fails the turn with an explicit reason instead of leaving false busy
state. Regression coverage proves both the no-final-repaint completion path and
the control-stream-closed path with a durable response.

After restarting the backend:

1. Open a chat whose Codex, Claude Code, Cursor, or Pi main terminal is retained.
2. Send a follow-up and verify the HTTP response remains the fast
   `sent_to_cli` path.
3. Verify Raw mode and Formatted mode show the same final assistant response.
4. Verify exactly one non-empty `unified_completion.final_result` is persisted
   for the retained turn.
5. Verify the session settles to completed while its provider tmux remains live
   and reusable for the next message.
