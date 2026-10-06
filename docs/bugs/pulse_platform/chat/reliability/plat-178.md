[← platform / chat-reliability](index.md)

# PLAT-178 — retained chat delivery and transcript recovery can leave the chat UI behind the terminal

| Field | Value |
|---|---|
| State | open |
| Priority | P1 |
| Product | chat |
| Area | reliability |
| Summary | owns durable live-input message persistence and native-transcript recovery when the chat UI falls behind the coding terminal. |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | `2026-09-23 stale-promotion jam fixed on main (evidence gate, 30m fail-after, dead-pane fast-fail); not deployed; live CLI verification pending` |
| Last synchronized | `2026-09-24` |

- **Priority:** P1 — real conversation data loss, user-visible and
  confusing (the agent appears to "forget" recent work and asks the user to
  re-explain things it already did), and the recovery source needed to fix
  it is already sitting on disk unused.
- **Owner:** conversation persistence (`cmd/server/chat_history_persistence.go`,
  `cmd/server/server.go`'s full-turn save block), terminal/session restore
  (`cmd/server/chat_history_routes.go`), Claude Code transcript reader
  (`multi-llm-provider-go`'s `claudecode_transcript_messages.go`).
- **Related:** [PLAT-177](../../coding-agents/bridge/plat-177.md)) (same session, same resume boundary,
  different symptom — tool-access confusion vs. this ticket's conversation
  data loss; investigated together, filed separately since the root causes
  are unrelated).

## 2026-09-22 follow-up — one durable dispatcher for every conversation producer

The rapid-send fix made pending messages visible sooner, but delivery was still
split across `/api/query`, `/live-input`, Crew chat, Workflow Builder chat, and
server-owned schedule, trigger, and bot starts. Each path could independently
decide whether to inject into an occupied CLI, wait, or start another turn. That
made the correctness of a conversation depend on which producer submitted the
message.

A workspace-backed FIFO dispatcher now owns this boundary for all conversation
turns. When a session is occupied, it persists the complete turn request in
`conversation-turn-dispatch.json`, records the user message immediately as a
structured event with `queued_for_turn`, and starts it only after the current
turn releases the shared session lane. The browser displays an amber queued
receipt and queue position; confirmation or failure updates that same durable
message identity. Queue recovery runs before schedulers start after a server
restart. Persisted requests exclude decrypted secrets and API keys, which are
resolved again when execution begins.

The matrix covers regular chat, Crew, Workflow Builder, live input, schedules,
triggers/webhooks, and bots. Retained CLI input is no longer a separate hidden
queue when the active CLI is busy. Headless workflow execution is intentionally
outside this dispatcher because it is an execution job rather than a
conversation turn; its existing scheduler remains authoritative. Automatic
completion notifications likewise retain their existing durable queue.

### Live finding: the old browser lane could freeze every later send

Before this dispatcher was deployed, RTS showed messages as permanent single
ticks while the server received no corresponding external `/api/query` or
`/live-input` request. The browser had already rendered each optimistic row,
but `chatSubmissionLane` put its network mutation behind an older unresolved
promise. Because the lane had no durable state or expiry, one hung preparation
or HTTP request caused unbounded head-of-line blocking for every later message
in that conversation.

The frontend lane has now been removed. Every user send starts its HTTP request
immediately, while the backend dispatcher provides the one authoritative FIFO,
durability, idempotency and restart recovery boundary. Exact rapid duplicate
clicks remain coalesced, but distinct user turns are never held only in browser
memory. The structured-source regression explicitly rejects reintroducing
`chatSubmissionLane` in the chat submission path.

The final frontend exception was removed at the same time: the queued-message
**Steer** action previously called `/api/sessions/:id/live-input` directly.
Enter, Send, queued decisions, report actions, and Steer now all submit through
`/api/query`; only the backend decides whether the message starts a turn, joins
the active provider, or waits in the durable FIFO. The `/live-input` server
route remains temporarily as a compatibility shim for older already-loaded
clients, with no caller in the current frontend.

## 2026-09-21 recurrence — rapid messages were accepted but appeared late

RTS session `c7d58080-0058-4f6f-a394-feea651f405b` showed that several
messages sent while a retained Cursor/tmux turn was active did not appear in
Formatted Chat immediately. Server logs confirmed that delivery was ordered
and lossless, but each `/api/query` occupied the frontend's per-session promise
lane for 15–19 seconds while Cursor waited for a safe live-input boundary. The
frontend created its optimistic user row *inside* that lane, so the second and
later messages were invisible until every preceding request completed.

The frontend now stages each established session's optimistic user row before
entering the serialized delivery lane. The lane still owns network mutation
order and duplicate coalescing; a rejected submission removes its unstamped
optimistic row, while an accepted row continues through the existing durable
message-ID and delivery-receipt reconciliation. Focused regression tests cover
the visibility-before-enqueue invariant, promise-lane ordering, and duplicate
live-input coalescing. TypeScript production build passes; targeted ESLint has
no errors (two pre-existing `ChatArea.tsx` hook warnings remain). Deployment
and live rapid-multi-send verification are pending.

This is intentionally a visibility fix, not a fabricated fast delivery
receipt. The [durable acknowledgement contract](../../../../refactor/durable_ack_p0.md)
requires Cursor's safe-composer transport acceptance before the single tick and
`store.db` evidence before the double tick. The observed 15–19 second interval
was before transport acceptance, not the asynchronous durable-ack watcher; the
contract explicitly rejects returning success before that attempted delivery.

## Symptom

On workflow "substack" (session `b5e39872-4e4e-4645-8059-6d6e7a1231db`), the
user resumed a chat and the agent presented an old assistant message ("Yes —
both bugs you reported are fixed... I also added a new 'Follow Health'
panel... still want to check the `step-connect-creators` wording?") as if it
were the current end of the conversation. It was not — the user had
continued the conversation well past that point. The user: "this is not the
final assistant response.. this is like the 2nd msg... what don't we resume
properly."

## Root cause, confirmed by direct evidence

**1. `conversation_history` is a one-shot snapshot, written only when a full
turn completes — not continuously as a live tmux CLI session runs.**

The save happens once, at the end of the streamed `/api/query` handler,
after a turn completes:
- `[CONVERSATION DEBUG] Native continuation merge: ...` — `cmd/server/server.go:6233`
- `[CONVERSATION DEBUG] Final save: ...` — `server.go:6236-6238`
- `[BUILDER LOG] Saved conversation log (752 messages) to
  Workflow/substack/builder/conversation/2026-08-22/session-b5e39872-...-conversation.json`
  — `server.go:6358`, inside the `isWorkflowPhase` block (`server.go:6326-6363`).

Live: this fired at `13:51:26` on 2026-08-22, saving exactly 752 messages
ending with the "Follow Health panel" reply. The file's mtime on disk is
`13:51:26` and it was never written again.

A structurally identical one-shot writer exists for background "synthetic"
turns (`cmd/server/background_agents.go:2394-2435`). Neither path is
periodic; the only ticker in this area
(`conversation_turn_lifecycle.go:317`, 250ms) is a scheduled-message
completion poller, unrelated to persistence.

**2. Once a session is "warm" (a live tmux pane already exists — exactly the
state right after any resume), further messages go through `/api/live-input`
instead of a full turn, and that path only ever persists the user's own
outgoing message, never the assistant's reply.**

`/api/live-input` (`server.go:8342-8477`) delivers directly into the
retained tmux session (`retainedSession.Send`,
`deliverRetainedMainTerminalInput`, `mcpagent.DeliverAgentInput`) whenever
`sessionHasLiveMainCodingTmux` is true (`server.go:8390`) — bypassing the
full-turn handler entirely, so it never reaches the save block in finding
#1. Its only persistence hook is
`appendLiveInputToPersistedChatHistory` (called at `server.go:8353,8370`,
defined `chat_history_persistence.go:2934-3009`), which reads the existing
JSON, and appends **only**:

```go
history = append(history, llmtypes.MessageContent{
    Role:  llmtypes.ChatMessageTypeHuman,
    Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: message}},
})
```

(`chat_history_persistence.go:2976-2979`) — hardcoded
`ChatMessageTypeHuman`. The CLI's assistant replies to these live-input
turns are never written back. Live: the user's session went straight into
this warm state after the 13:51:26 save (confirmed by
`sessionHasLiveMainCodingTmux`-gated CDP browser activity at `13:55:04` and
raw terminal polling through at least `14:31`), so nothing after 13:51:26
ever reached `conversation_history` — even though real, substantial
conversation happened.

**3. When the tmux pane later dies and the session needs to restore, the
fallback tier trusts that same stale snapshot with no attempt to recover
anything richer.**

`chat_history_routes.go:155-176` restore logic: tier `attach_existing`
(`chat_history_routes.go:250-271`) tries to reattach to the live tmux pane
via `captureTerminalPane`; live, this failed at `16:18:20` with
`reason=tmux_session_not_running` (the pane was gone by then). It falls
through to tier `persisted_snapshot`
(`chat_history_routes.go:273-301`/`selectPersistedTerminalSnapshot` at
`chat_history_routes.go:303-314`), which reads only the `terminal_snapshots`
already embedded in that same stale conversation JSON — i.e. the
13:51:26 state. No tier consults tmux scrollback, a separate transcript
log, or the CLI's own on-disk session file; the comment at
`chat_history_routes.go:141-153` explains the two launch-based recovery
tiers are deliberately skipped here (a real, separate constraint — avoiding
a tool-registration race by deferring launch to the next `/api/query`) —
that constraint is legitimate but unrelated to *this* gap, and no other
tier was added to compensate for it.

**4. The full, missing conversation genuinely exists on disk — the restore
path just never reads it.**

Claude Code CLI writes its own transcript independent of agent_go's JSON,
at `~/.claude/projects/<encoded-cwd>/<session-id>.jsonl`
(`multi-llm-provider-go/claudecode_transcript_path.go:10-20`). Confirmed
live at
`~/.claude/projects/-Users-mipl-ai-work-mcp-agent-builder-go-workspace-docs-Workflow-substack/71ed3dcb-fe3c-412d-94cb-e62226fc4648.jsonl`
(`external_session_id` from the session's own `runtime.agent_session_handle`)
— **155 entries timestamped after 13:51:26**, including 36 real user
messages, 63 real assistant messages, 37 attachments, and 3 system entries,
running until a final `<local-command-stdout>Bye!</local-command-stdout>`
entry at `15:19:59` local time. None of this reached
`conversation_history`; all of it is still sitting in this file, unread by
the restore path.

`readClaudeTranscriptMessages`
(`multi-llm-provider-go/claudecode_transcript_messages.go:43-134`) already
knows how to reconstruct full `[]llmtypes.MessageContent` (text, tool_use,
tool_result) from exactly this file format, but it is only ever called from
`claudecode_interactive_adapter.go:664`, scoped to a single turn's
`turnStart..now` window — never from the resume/restore path.

## Why this matters beyond the one incident

Any session that (a) resumes into a warm/retained tmux state — which is the
normal case, not an edge case, since a resumed session almost always still
has its tmux pane alive initially — and (b) exchanges any further messages
purely through `/api/live-input` before its tmux pane eventually dies for
any reason (intentional `/bye`, crash, terminal reap, host restart) will
have that entire stretch of conversation permanently invisible to
`conversation_history`, `costs`, and every UI/API surface that reads it —
even though Claude Code's own transcript still has it. This is not specific
to the substack workflow or to this one incident.

## Fix implemented

Four independent gaps were identified. All four are now implemented:

1. **Persist every provider-confirmed live-delivery user message.** Both
   `/api/query` retained delivery and `/api/live-input` now call the same
   `persistLiveInputUserMessage` helper after the coding CLI confirms receipt.
   This closes the 2026-09-16 Confida recurrence where session
   `61247bd2-347b-4776-982f-e158e84f9ad6` delivered the user's message to Pi
   and completed in the terminal, but the durable JSON stopped before the last
   two turns. The helper is reached by durable `mcpagent`, cold retained-tmux,
   and running-Agent delivery paths and keeps the authenticated internal user
   ID when writing history.
2. **Make the workflow-builder restore path read the native transcript**
   (finding #3/#4) — **implemented**. `restoreLatestBuilderConversation`
   (`workflow_builder_session_routes.go:215-`, the actual function that
   serves the stale snapshot to the chat UI when no live in-memory session
   matches — confirmed to be the real serving path, not the terminal-pane
   tiers in `chat_history_routes.go`, which restore the separate raw
   terminal widget) now calls
   `refreshLatestBuilderConversationFromNativeTranscript`
   (`claude_native_transcript_sync.go`, new file) on the winning candidate
   before converting it to display events. For `provider=claude-code`
   sessions with a resolvable native session ID and working directory (read
   from the snapshot's own `runtime.agent_session_handle.provider` block),
   it locates Claude Code's own JSONL transcript
   (`~/.claude/projects/<slug>/<session-id>.jsonl`, using the same
   working-directory-to-slug escaping scheme as
   `multi-llm-provider-go/claudecode_transcript_path.go`, duplicated rather
   than imported since that resolver is unexported), reads the full native
   transcript, and sequence-merges it with persisted history. It does **not**
   use the snapshot's `updated_at` as a transcript cursor: persisting a later
   live-input human message advances that field before the preceding assistant
   reply is saved, so a timestamp cutoff can permanently skip the reply and
   duplicate the later user message. The merge preserves persisted-only prefix
   messages, uses shared ordered messages as anchors, inserts native-only
   replies between them, and deduplicates messages present in both sources. It
   keeps only real chat text (plain-string user content, `text`-typed assistant
   blocks — `tool_use`/`tool_result`/`thinking` are filtered out as execution
   detail, not something either party said), and best-effort persists the merge
   back to the same file so later reads see it too. A failed persist (e.g.
   the workspace API being unreachable) is logged but never blocks the
   restore itself — the in-memory catch-up is still served.

   The Pi branch now resolves its native transcript with
   `picli.ReadNativeTranscriptFromWorkingDir(workingDir, nativeSessionID)`.
   The earlier default-store lookup could not see transcripts inside the
   session's isolated runtime directory, so completion synchronization retried
   three times and found no completed reply even though the JSONL contained the
   missing user message and full assistant answer.

3. **Preserve Pi thinking blocks as retained-turn progress.** A second live
   check on the same Confida session used the message “can you group them by
   companies”. The terminal showed many narrated updates, including
   “Identifying Session Clues” and “Mapping Company IDs”, while the chat showed
   only “41 tool calls” until the final answer. Direct inspection of Pi's JSONL
   proved those updates were `thinking` blocks paired with tool calls and empty
   `text` blocks. Normal Pi streaming already maps `thinking_delta` to visible
   assistant progress, but `ReadRetainedTurnProgressMessages` rebuilt progress
   only from `text`, silently dropping the retained equivalent. The transcript
   summary now keeps a separate `ProgressMessages` projection containing both
   thinking and text blocks in order. The final-answer `Messages` projection is
   unchanged, so thinking can never satisfy retained-turn completion or appear
   as the final answer.

4. **Always reconcile interactive workflow chats with durable history.** A
   later reproduction in the same Confida session exposed a frontend-only
   restore race after the backend fixes above had correctly persisted the
   answer. The terminal contained the completed judge scorecard (`PASS`,
   `0.95 / 1.0`, seven Langfuse scores and `judge_scorecard.md`) and the durable
   `conversation_history` also contained that answer plus the next
   `dont add into langfuse` exchange, while Chat stopped at the older
   “workflow execution is currently running” response. The persisted
   `ui_events` array was capped at 200 and ended before those durable turns.
   Workflow restoration treated any non-empty volatile event array as fully
   hydrated, so it never requested the authoritative conversation history.

   Interactive workflow tabs now reconcile with durable history even when a
   volatile event tail is present; `hydrateTabEvents` then deduplicates and
   merges that live tail so tool/progress events remain visible. Entering
   Formatted view also performs the same reconciliation, covering refreshes
   that initially reopen in Terminal view. Read-only schedule tabs retain the
   cheaper empty-only hydration rule.

## Why this required several fixes

The user-visible symptom — “the terminal has the answer but Chat lost it” —
can be produced independently at four ownership boundaries:

1. the retained coding-agent delivery path can accept a user message without
   appending it to the server's durable chat;
2. the provider's native transcript can contain the completed answer while
   server recovery looks in the wrong working directory;
3. the provider can represent visible progress as `thinking` rather than
   `text`, causing retained progress reconstruction to omit it; and
4. the server's durable chat can already be correct while the frontend treats
   a non-empty, bounded `ui_events` tail as a complete transcript and never
   reconciles the durable messages.

These are separate data paths that converge on the same screen, so closing one
does not prove the other three are closed. The invariant established by this
ticket is: the provider transcript is recovery evidence, durable
`conversation_history` is authoritative chat state, and `ui_events` is only a
bounded execution/progress projection. A non-empty `ui_events` array must never
suppress durable-history hydration.

## Deployment and live verification

- Deployed application revision
  `912b9347e473d078f3c82b72c1bf10a9fd1ebedc` as Confida release
  `confida-912b9347-20260916083842` on 2026-09-16. The release pipeline
  reported the agent API healthy, the public endpoint returned HTTP 200, and
  the `confida-agent`, `confida-gateway`, and `confida-workspace` user services
  were active after activation.
- Live browser verification after a hard reload restored the durable Chat
  history rather than stopping at the stale capped UI-event tail. The UI
  displayed the previously missing local-only Langfuse configuration reply
  and the complete trace/business-context response. This confirms the deployed
  Formatted-view hydration is reading and merging authoritative history.
- The same reload also confirmed the bundled toolbar changes: Knowledgebase is
  top-level, Costs is under Ops, and Playbooks is under Setup.
- Provider-matrix verification covers every registered persistent live-input
  CLI: Claude Code, Codex CLI, Cursor CLI, Pi CLI, and Muse. Shared user-message
  persistence and frontend durable-history reconciliation are provider-neutral;
  each provider has retained-progress/final-answer readers and a native
  transcript recovery branch. Focused adapter, retained-session, and server
  recovery suites passed across all three repositories on 2026-09-16. A
  contract-derived regression now fails if a future persistent live-input CLI
  is registered without declaring transcript reading or without being included
  in native chat-history recovery.

## 2026-09-16 13:40 IST recurrence report

Conversation `e4f5abb1-57f3-4ba8-823e-87b3fe2e2a93` is a direct reproduction
of gap #4 after the fixed release had reached the server. The operator
confirmed the page had been refreshed, ruling out a pre-deployment frontend
bundle still running in memory:

- At 13:40:07 IST, the Formatted view stopped at intermediate progress
  (“Defining Post-Execution Report”, “Defining Next Steps”, and “Testing the
  Implementation”). At 13:40:21 IST, the Terminal view already displayed the
  completed response.
- The production conversation JSON had been updated at 13:38:31 IST and held
  257 durable `conversation_history` messages, including the complete final
  “I have created the dedicated scripted step...” answer. Its 43 `ui_events`
  ended earlier. The answer therefore was not lost by the server; the formatted
  projection was stale because it trusted the non-empty UI-event tail.
- Revision `912b9347e473d078f3c82b72c1bf10a9fd1ebedc`, which implements the
  unconditional interactive-chat reconciliation, was packaged as
  `confida-912b9347-20260916083842`. Confida's release names and filesystem
  timestamps use the host's UTC+02 timezone: 08:38 host time is 12:08 IST,
  about 91 minutes before the screenshots — not 14:08 IST as initially
  recorded. The current release, `confida-7def0915-20260916090324`, was active
  by 12:36 IST and includes that revision.
- The follow-up reconciliation has an ordering defect. `hydrateTabEvents`
  restores durable conversation events first, filters duplicates from the
  in-memory event response, and then calls `addTabEvents` with the remaining
  volatile tail. `addTabEvents` is append-only and Formatted view preserves
  array order. Therefore older, non-carrier progress events can be appended
  *after* the newer durable final answer. The newest answer is no longer the
  bottom item even though hydration fetched it successfully, producing the
  same apparent “final message lost” symptom.
- This incident has the exact dangerous shape: the 43 persisted UI events end
  on the earlier 13:00 IST turn, the 257-message durable history ends with the
  13:38 final answer, and the screenshot bottom shows intermediate progress
  rather than that final answer. The existing regression test checks that
  durable history is requested, but does not check final ordering after a
  non-carrier live progress tail is merged.

Classification: **confirmed post-deployment frontend regression; no backend
message loss.** The first PLAT-178 frontend fix corrected the decision to fetch
durable history, but did not enforce chronological/turn-relative ordering when
combining that history with the live event tail. The follow-up must add an
ordering regression test with a durable final answer plus older live progress,
then merge the progress before its matching final carrier rather than blindly
appending it.

## Follow-up reconciliation refactor

Implemented and deployed on 2026-09-16:

- `hydrateTabEventsFromConversation` is now the single ordering boundary for
  persisted UI events, raw events already received through SSE, and the current
  EventStore window. All three sources enter `conversationToRestoredEvents`
  together, so its turn anchors and chronological projection apply once.
- Removed both post-hydration append paths: normal page hydration and the
  existing-tab restore path no longer call `addTabEvents` after constructing
  durable history. Legacy sessions without durable history keep their existing
  live-only fallback.
- Repeated hydration filters its own synthesized events before rebuilding the
  trace, preventing generated timestamps from feeding back into the next
  projection.
- Durable history still paints before a slow live-status request completes;
  when the live response arrives, the same projection function reconciles it.
- The shared carrier extractor now handles both nested `{data:{content}}` and
  legacy flat `{content}` event envelopes after restore metadata is attached.
  The broader product fallback suite exposed this pre-existing deduplication
  hole as a duplicate user row during the refactor.
- Added regressions for the production ordering shape (older live progress plus
  a newer durable final answer) and flat-envelope carrier deduplication.

Verification: 53 focused restore/conversation tests pass, TypeScript project
compilation passes, focused ESLint passes, and `git diff --check` is clean.
Commit `84a102b2c4de39c1472d3ad7eda686c281257df9` was pushed to `main` and
deployed as Confida release `confida-84a102b2-20260916134610`. The deployment
pipeline passed release-asset and bundle-budget checks; `confida-agent`,
`confida-gateway`, and `confida-workspace` were active; internal agent health
and the public endpoint returned 200; workspace health was `healthy` with the
Landlock sandbox available. A hard-refreshed fresh retained turn remains the
final user-visible verification.

## 2026-09-21 queued-turn recurrence

A Sheets Analysis chat showed the terminal and Formatted Chat disagreeing about
the same retained Claude session. In project chat session
`work:project:be2a4cf4-0f3b-49f7-a3fb-084f5f8028c4` (Claude native session
`ce286440-2463-442d-84f6-fbce00e88e2a`, terminal
`mcp-agent-20260921-001313`), the terminal chronology correctly showed the user
asking "si sheet111 tab used for anything?" between two assistant turns. The
durable conversation omitted that user prompt: its final eight
`conversation_history` entries were all assistant messages, while the capped
UI-event tail ended before the prompt. Formatted Chat therefore grouped stale
assistant updates around the newest reply and made old content appear current.

The native Claude JSONL retained the missing boundary, but represented it as a
human `queued_command` attachment rather than a normal `user` entry: a
`queue-operation` enqueue was followed by an attachment with
`type: "queued_command"`, `humanTurn: true`, and `origin.kind: "human"`, then
the final assistant response. The backend transcript reader accepted only
normal `user`/`assistant` entries, so it silently discarded the queued user
turn and merged consecutive assistant updates across the lost boundary.

Two complementary fixes are now on `main`:

- `44c727fc3` makes restored event identities content-bound and only applies
  the `prependedIndex` shift when the old tail was actually preserved. This
  prevents React/Virtuoso row reuse from displaying stale content when a
  bounded history page moves during hydration.
- `eb92d4570` recovers only genuinely human `queued_command` attachments,
  unwraps their `<pasted_content>` payload, ignores non-human attachments, and
  inserts the recovered user message into sequence merging so the turn
  boundary cannot disappear.

Regression coverage includes the parser and sequence merge for the missing
human boundary, restored-event identity changes, and bounded-history scroll
reconciliation. Focused Go tests pass; all 31 frontend
`sessionRestore`/`useTranscriptScroll` tests pass; lint, Go build, workspace
build, and Electron build pass. Both commits are pushed to `main`; deployment
and a fresh-client retained-turn check remain pending.

## 2026-09-24 — deploys and config edits silently replaced the native CLI session ("the agent forgot yesterday")

**Symptom (RTS, SDE crew `gptlive1`, claude-code):** "what did we work on
yesterday" — the crew did not know. Its native Claude session changed from
`3ccd4ab5…` to `c7f928e6…` at 2026-09-23 18:32 UTC, the first turn after the
18:06 deploy. The new session only received a one-shot "read the archive"
notice; with 1,509 archived messages it skimmed `chat_history/chat-index.json`
previews instead.

**Root cause — two fingerprints treated config drift as a new conversation:**

| Where | Fingerprint | What changed it |
|---|---|---|
| Crew / product chats | `agentProfileSessionKey` (profile Definition + selected MCP servers + identity); `seedCodingAgentRuntimeFromRestoredConversation` skipped native resume on mismatch | any deploy touching the Work system prompt or tool allowlist; adding an MCP server |
| Workflow Builder/Run chats | `chatPolicySessionKey` = role (mode, origin, capabilities) **plus** chat definition + MCP config + user config; any difference → `[CHAT_POLICY] Policy refresh … starting a fresh native coding-agent session` | any deploy touching the workflow chat prompt; any MCP/user-config edit |

**Fixes (on main, not yet deployed at time of writing):**

1. `eb18a0e39` — Crew/product chats resume the same native session when the
   profile definition changes; the stale retained process is still refused
   live input, which forces a relaunch with the current definition.
2. `e2e745939` — workflow chats: new `ChatPolicyRoleKey`; only a real role
   change (Builder↔Run, origin, capabilities) replaces the session.
   Definition/config drift keeps it. Legacy runtimes without the role key
   resume (mode still compared).
3. `5dbe11a83` — when a fresh session is unavoidable, the continuity notice
   carries the recent user/assistant dialogue (shared `recentDialogueLines`)
   plus the archive path with "search it"; the Work prompt says where full
   history lives.
4. Per-CLI verification that a resumed session actually receives the NEW
   instructions (rule changed ALPHA→BRAVO between two turns of one session):

   | CLI | How instructions arrive | Follows new rule after resume? |
   |---|---|---|
   | claude-code | CLAUDE.md rewritten per launch; recorded as a fresh `instructions` attachment on resume (seen in the SDE transcript at 18:31 and 03:20) | yes |
   | codex-cli | AGENTS.md | yes (`codex exec resume`, local 0.156.1) |
   | pi-cli | `--append-system-prompt` every launch | yes (local 0.87.1) |
   | cursor-cli | `.cursor/rules` (tmux) / first message only (structured) | **no** — fixed in multi-llm `37b0666`: resend a changed prompt inline once per native chat (sha256 marker); verified on RTS, later turns keep following it |
   | muse-cli | AGENTS.md | **no**, and an inline override is ignored too → `e29ac0384`: for muse-cli only, instruction drift starts a fresh session with the recent dialogue (`codingProviderReloadsInstructionsOnResume`) |

   Tools were never affected: every adapter regenerates its MCP config per
   launch and crew tools are fetched live via `get_api_spec`.

**Not verified yet:** a live post-deploy check that crews and workflow chats
keep their native session across the next deploy (look for `[CHAT_HISTORY]
… resuming the same native coding-agent session` instead of `Native
coding-agent continuation unavailable`). The SDE crew's memory before
2026-09-23 18:32 cannot be restored natively; it is intact in
`builder/conversation/`.

## 2026-09-23 queued-turn recurrence — stale promotion jams the lane forever

Local Work chat session `work:project:75db2aa1-a719-4365-a871-824d74b6930a`
(muse-cli over a retained tmux pane, multi-agent mode): two submits —
"yes" at 21:01:08 and a ULIP-investment review at 21:01:27 IST — were
durably queued with positions 0/1 and stayed `queued` in the UI
indefinitely. No replies ever arrived. The queue doc
(`workspace-docs/_users/default/chat_history/conversation-turn-dispatch.json`)
still holds both turns with no `StartedAt`: never claimed, never
executed. The UI was telling the truth — the backend accepted the turns
and then never ran them.

### Timeline (IST, `agent_go/logs/server_debug.log`)

- 20:00–20:42 — normal retained turns over tmux, each settling via
  `Settled retained main-agent turn from structured unified_completion
  event` (20:18:50, 20:35:01, 20:42:07). Note every settle in this session
  carries a non-empty `next_execution`: the promotion branch fires on
  every completion, not just rapid-fire ones.
- 20:40:36 / 20:40:39 — rapid-fire submits ("setup purpose yourself",
  "you know the different dashboards") while a response is active; their
  execution IDs stack onto `retainedMainTurnPendingExecutionIDs`.
- 20:42:07 — settle logs `state=completed
  next_execution=query_1790173821969293000`. The `1790173821…` timestamp
  embedded in that ID is 20:00:23 — the same ID appears in the 20:00:23
  `[CODING_AGENT_DELIVERY]` line. So the "next" execution is a turn that
  completed ~42 minutes earlier. It survived two intervening settles
  (20:18, 20:35 promoted other IDs) because nothing ever prunes the
  pending list — entries only leave via promotion or the failed-state
  branch.
- 20:42–21:01 — gap census for the session shows only the settle, one
  chat-history persist, and timeline echoes. No new turn starts, no API
  submits, no delivery. The lane goes busy with no live turn behind it.
- 21:01:08 / 21:01:27 — both POSTs return 200 in ~150ms with
  `status=accepted, delivery_status=queued_for_turn` (+ position). The
  frontend stamps the clock tick purely from that response
  (`ChatArea` → `withLiveInputReceipt`; there is no optimistic
  client-side queued state — verified by code search and `git log -S`).
- 21:12:07 — `[RETAINED_TURN] WARNING: turn still observed with no
  pane-idle and no durable final response … elapsed=30m1s quiet=7s`.
  Elapsed counts from the 20:42 re-arm, confirming the watcher has been
  waiting on the phantom ever since.

### How the jam holds (mechanism)

1. The 20:42 settle takes the promotion branch
   (`observeRetainedMainTurnEvent`, `server.go` ~8686–8704): pending is
   non-empty and state is not failed, so it promotes `pending[0]`,
   re-arms `retainedMainTurns[sessionID] = time.Now()`, starts a fresh
   `observeRetainedMainTurnStream` watcher, and returns with
   `promotedExecutionID != ""`.
2. Because promotion happened, the settle skips
   `kickConversationTurnQueue` (`server.go:8760-8762`) and re-marks the
   session busy/running. Correct for a live next turn; fatal for a phantom.
3. The watcher (`observeRetainedMainTurnStream`, `server.go:8362`) can exit
   only via ctx-cancel, stream close/capture error, durable-sidecar
   settle, or pane-ready settle. None can fire here: the retained tmux
   session is gone (`tmux ls` shows only an unrelated Sept-19 session;
   only an orphaned `tmux -CC attach` process from 20:42 remains), the
   muse CLI session (`01a0ce94-…`) has been idle since ~19:37 (last writes
   are `resource_pressure.observed` noise), and the durable sidecar holds
   no *new* final response — the completion for this execution ID already
   happened at 20:00. The 30-min stuck warning (`retainedMainTurnStuckWarnAfter`)
   is log-only; the loop then `continue`s forever.
4. `conversationTurnOccupied` (`conversation_turn_queue.go:184`) stays true
   via the re-armed `retainedMainTurns[sessionID]` entry (one of six
   checks; the others are lane token, dispatch-pending, active cancel,
   stored turn flag, retained `ActiveTurnID`). Every kick — at enqueue,
   at every later submit, from every completion hook — returns early at
   the occupancy guard. The queued turns sit unclaimed; no failure is ever
   recorded, so the UI shows `queued`, never `failed`.

Net: a single stale-string promotion converts the lane into a permanent
busy signal with no timeout and no failure path. Every later submit in
that session queues behind nothing, forever.

### Evidence (verbatim — the diagnosing machine's logs are not shared)

Environment: local dev server on the reporter's laptop
(`run_server_with_logging.sh --with-workspace`; agent `:18743`,
workspace `:18744`, docs-dir `workspace-docs`), Work product chat,
user `default`, session `work:project:75db2aa1-a719-4365-a871-824d74b6930a`,
provider `muse-cli`, transport tmux, retained terminal
`…:main:work:project:75db2aa1-…`. All times IST (UTC+5:30) on 2026-09-23.

Stale ID origin — 20:00:23 delivery line whose JSON contains
`"query_1790173821969293000"` (ID suffix `1790173821…` = 20:00:23 UTC+5:30):

```
2026/09/23 20:00:23 [username=user workflow=new-project-75db2aa1 mode=multi-agent …] [CODING_AGENT_DELIVERY] {"cli_accepted_at":"2026-09-23T14:30:23.414383Z",… "query_1790173821969293000"…}
```

Phantom promotion — 20:42:07 settle promotes that same 42-minute-old ID:

```
2026/09/23 20:42:07 [RETAINED_TURN] Settled retained main-agent turn from structured unified_completion event session=work:project:75db2aa1-a719-4365-a871-824d74b6930a terminal=work:project:75db2aa1-a719-4365-a871-824d74b6930a:main:work:project:75db2aa1-a719-4365-a871-824d74b6930a state=completed next_execution=query_1790173821969293000 username=user workflow=new-project-75db2aa1
```

Queued submits — both POSTs return the small `accepted` JSON in ~150–217ms
(full-body submit path would take seconds and emit delivery lines):

```
2026/09/23 21:01:08 […] [API] <-- POST /api/agent-profiles/work/query request_id="-" status=200 bytes=299 duration=217ms in_flight=0
2026/09/23 21:01:27 […] [API] <-- POST /api/agent-profiles/work/query request_id="-" status=200 bytes=299 duration=157ms in_flight=0
```

Watcher warning — elapsed counts from the 20:42 re-arm (30m1s):

```
2026/09/23 21:12:07 [RETAINED_TURN] WARNING: turn still observed with no pane-idle and no durable final response session=work:project:75db2aa1-a719-4365-a871-824d74b6930a terminal=work:project:75db2aa1-a719-4365-a871-824d74b6930a:main:work:project:75db2aa1-a719-4365-a871-824d74b6930a provider=muse-cli elapsed=30m1s quiet=7s username=user workflow=new-project-75db2aa1
```

Queue doc (both turns intact, neither claimed — `StartedAt` absent):

```json
{
  "version": 1,
  "turns": [
    {"id": "963aaea7-d26f-4c8b-b637-50479b5e5e7e", "user_id": "default",
     "session_id": "work:project:75db2aa1-a719-4365-a871-824d74b6930a",
     "request": {"query": "yes", "provider": "muse-cli", "agent_mode": "multi-agent", …},
     "submission_id": "c07aa81d-7b2d-4528-bd26-b0b942d00936",
     "created_at": "2026-09-23T15:31:08.8654Z"},
    {"id": "2ae0b9d2-96cc-464d-88d6-58acb7811c23", "user_id": "default",
     "session_id": "work:project:75db2aa1-a719-4365-a871-824d74b6930a",
     "request": {"query": "also can you review my ulip invesments.. i added some new", …},
     "submission_id": "47d70eba-145d-4db2-9a35-9bc08a5d3b84",
     "created_at": "2026-09-23T15:31:27.53754Z"}
  ]
}
```

Dead pane — no tmux session for the retained terminal (only an unrelated
Sept-19 session; the work session's attach process from 20:42 is orphaned):

```
$ tmux ls
mlp-muse-query-1789836676715690000: 1 windows (created Sat Sep 19 22:21:18 2026)
$ ps … | grep tmux
76808 … 0:00.01 tmux -CC attach -t mlp-muse-work-project-75db2aa1-a719-4365-a871-82…
```

Idle CLI — retained muse session `01a0ce94-1149-7842-acdc-c32571a8314c`
(dir mtime 19:37 IST; tail is only `session.resource_pressure.observed`
noise, no turn activity since ~19:37).

Gap census — every log line for the session in 20:42–21:01 is one of:
the 20:42:07 settle, one chat-history persist, three timeline echoes.
No turn start, no submit, no delivery. The lane went busy with nothing
behind it.

Occupancy gate that never clears (`conversation_turn_queue.go:184`):

```go
if api.sessionTurnInProgress(sessionID) || api.conversationTurnDispatchPending(sessionID) ||
    api.hasActiveTurnCancel(sessionID) || api.storedAgentTurnInProgress(sessionID) {
    return true
}
if retained, ok := mcpagent.LookupSession(sessionID); ok && retained.ActiveTurnID() != "" {
    return true
}
_, retainedRunning := api.retainedMainTurns[sessionID]  // ← stuck entry
return retainedRunning
```

### Originally proposed fix (superseded — see Review corrections and Implemented below)

1. **Promotion validation (root cause).** Before promoting `pending[0]`,
   drop leading entries whose embedded creation time predates the
   completing turn's `startedAt` (already in scope at the promotion
   branch), logging each drop. Anything older than the turn that just
   completed cannot be live input the CLI is about to answer — legit
   rapid-fire pendings are seconds old, so this cannot misfire on the
   real case. If nothing fresh remains, fall through to the existing
   delete-tracking + release + kick path.
2. **Watcher fail-after (defense in depth).** Add
   `retainedMainTurnStuckFailAfter` (e.g. 60–90 min, a multiple of the
   30-min warn threshold so genuine long turns survive); past it, emit a
   failed stream completion and return. The existing `failed` branch
   already deletes all four tracking maps, releases the lane, fails
   pending IDs, and kicks the queue — queued turns then drain and the UI
   flips to failed-with-reason instead of hanging. Thresholds are already
   package vars mutated in `terminal_live_attach_test.go`, so this stays
   testable the same way.
3. **Dead-pane fast-fail.** In the watcher recheck, test `tmux has-session`
   for `snapshot.TmuxSession`; after N consecutive misses (3, to ride out
   transient tmux hiccups), fail the turn immediately with reason
   `retained pane gone`. A dead pane can never yield pane-idle or a new
   durable response, so waiting is pure loss. (The capture-error →
   `handleRetainedMainTurnStreamClosed` path covers *some* pane death, but
   this incident captured successfully while the session was gone, so an
   explicit existence check closes the hole.)

Tests: promotion skips stale pendings and frees the lane; watcher fails
after the fail-after threshold (var override); dead-pane fast-fail after
consecutive misses while a single transient miss does not fail.

Immediate relief for a jammed session: restart the agent server.
Occupancy is all in-memory; startup recovery (`recoverConversationTurnQueue`)
resets `StartedAt`, re-registers queue owners, and kicks — unclaimed
turns in the queue doc then execute normally. Verified safe for this
incident: both turns are intact and unclaimed in the queue doc.

### Review corrections (2026-09-23)

- **The stale ID was not re-appended and did not "survive" pruning by
  accident.** The pending list is FIFO and each settle promotes exactly one
  entry. When a CLI answers several rapid inputs in one response, inputs are
  appended faster than completions pop them, so the list lags: the 20:18 and
  20:35 settles popped older entries and the 20:42 settle reached the 20:00
  input. Every promotion after the first batch was a phantom. No caller
  re-marks a finished execution (all callers pass a fresh ID per request);
  a guard now refuses to queue an already-finished execution anyway.
- **"Rapid inputs answered in one response" is the trigger**, not only stale
  IDs. It creates brand-new phantom pendings too, so the timestamp-based
  fix #1 would not have caught it, and parsing times out of execution IDs is
  fragile. Replaced by a promotion evidence gate.
- **Why only some providers jam:** Claude, Codex, Cursor and Pi report an idle
  composer, so a phantom promotion settles about 1s later on pane-idle. Muse
  has no pane-ready signal and relies on a durable final response written
  after the promotion, which a phantom never produces, so it waited forever.
  Here the pane had also vanished while the tmux control client stayed
  attached, so the existing stream-closed path never fired.

### Implemented (2026-09-23)

`agent_go/cmd/server/server.go`:
1. **Promotion evidence gate.** A promoted turn's observer
   (`observeRetainedMainTurnStreamMode(..., promoted=true)`) must see new CLI
   output. Without it for `retainedMainTurnPromotionEvidenceGrace` (75s),
   `settleUnansweredRetainedPromotion` completes the promoted input and the
   backlog behind it as answered by the previous response
   (`settled_reason=answered_by_previous_response`). It then emits the
   completion, which releases the lane and kicks the conversation-turn queue.
2. **Fail-after backstop.** After `retainedMainTurnStuckFailAfter` (30m) with
   neither pane-idle nor a durable final response, the turn and its backlog
   are failed (`failRetainedTurnExecutions`). The lane is released so queued
   turns drain, and the chat shows the reason.
3. **Dead-pane fast-fail.** Every `retainedMainTurnPaneCheckInterval` (5s)
   the observer checks the pane (`display-message`/`has-session`). Three
   consecutive missing or dead results fail the turn with
   `retained pane gone`; one transient miss does not.
4. **Guard.** `markRetainedMainCodingTurnRunningWithOwner` never queues an
   execution the tracker already finished (`trackedExecutionFinished`).

Tests in `plat178_retained_promotion_test.go` pass with `-race`, 3 runs:
- finished execution never queued;
- rapid inputs answered together settle after the grace window and release
  the lane;
- a promoted turn with new output is not settled early;
- fail-after drains the lane and fails the backlog;
- dead pane fails after 3 misses but not after 1;
- real-tmux killed pane releases the lane and fails the backlog.

In the real-tmux test, the existing stream-closed path fired because killing
the session also ends the control client. The orphaned-attach case is only
covered by the unit test.

The related suites (Retained, LiveAttach, LiveInput, ConversationTurn, Query,
Steer, CodingAgent, ChatSubmission, Durable, Terminal) pass except 4 tests
that fail identically on origin/main (`TestProfileQueryModelMustBelong...`,
`TestAutoPublishedCodingAgentLLMs*` ×2, `TestWorkshopResolveLLMConfig...`).

**Not verified:**
- A live Muse or Claude retained chat with rapid sends, and a real mid-turn
  pane kill on a running server.
- Whether an idle Muse TUI repaints on its own. If it does, that output
  counts as evidence and a phantom falls through to the 30m backstop
  instead of the 75s gate.

## Out of scope

- Not investigating why the tmux pane died in this specific incident — the
  transcript's own last entry (`<local-command-stdout>Bye!</local-command-stdout>`)
  suggests an intentional `/bye`/exit rather than a crash, but the fix
  needed here is the same regardless of why the pane ends.
- The existing tool-registration-race avoidance already documented at
  `chat_history_routes.go:141-153` is untouched — this fix lives entirely
  in the workflow-builder conversation-display path, a different function
  than the terminal-pane restore tiers that comment describes.

## Verification

The original PLAT-178 repair completed build, unit, deployment-health, and live
hard-reload verification. The follow-up reconciliation refactor above is now
deployed and healthy; it still requires a hard-refreshed fresh retained-turn
check before this recurrence can be closed.

- `go build ./...` clean.
- 2026-09-16 regressions pass:
  `TestTryDeliverQueryAsLiveInputReactivatesSettledRetainedTmux` proves a
  confirmed `/api/query` retained delivery persists the exact authenticated
  user/session/message tuple, and
  `TestRefreshLatestBuilderConversationFromPiTranscript` proves recovery reads
  the isolated working-directory transcript rather than ambient Pi storage.
- `TestPiRetainedProgressPreservesMessagesAroundTools` now includes a real
  thinking-only assistant record (`thinking` + tool call + empty text), proves
  it is emitted exactly once as progress, and proves it never enters the
  final-answer message stream. The shared retained-progress and completion
  watcher suites also pass for all retained providers.
- Frontend regression `workflowTabsNeedingHydration` proves an interactive
  workflow-builder tab with a non-empty capped volatile tail still requests
  durable history, while a populated read-only schedule remains untouched.
  The focused session-restore/hydration suite passes (19 tests), along with
  TypeScript compilation and focused ESLint.
- New tests in `claude_native_transcript_sync_test.go`: working-directory
  slug encoding matches the real scheme; text extraction correctly keeps
  plain-string/`.text`-block content and drops `tool_use`/`tool_result`/
  `thinking`; a fail-before/pass-after style test constructs a snapshot
  frozen at `13:51:26` plus a native-transcript fixture with two later
  messages and confirms the merge appends both in order and advances
  `updated_at` to the transcript's newest timestamp; a no-op test confirms
  non-`claude-code`/missing-runtime snapshots are left untouched.
- `TestRefreshLatestBuilderConversationIgnoresLiveInputUpdatedAtAsTranscriptCursor`
  reproduces the real ordering gap: persisted `user1,user2` with `updated_at`
  after native `assistant1`, and verifies restore returns
  `user1,assistant1,user2,assistant2` without duplication.
- Merge-level tests cover missing replies between persisted live inputs and
  repeated identical user messages with an older persisted-only prefix.
- `go test ./cmd/server/...`: all new tests pass; the only failures
  (`TestWorkshopResolveLLMConfigExpandsCodingAgentMode`,
  `TestStandalonePulseReviewCommandsUsePersistedReviewerPipeline`,
  `TestArtifactDriftAuditsTheSchedule`) are pre-existing on `origin/main`
  and unrelated to conversation persistence — confirmed by running the same
  three tests against `origin/main` directly before this change.
- Root cause itself was independently re-verified by direct evidence before
  any code was written: `conversation_history` JSON's mtime (`13:51:26`)
  confirmed frozen while the live session continued (CDP browser call at
  `13:55:04`, terminal polling through `14:31`);
  `appendLiveInputToPersistedChatHistory`
  (`chat_history_persistence.go:2934-3009`) read directly, confirmed it
  only appends the human message; the native Claude Code transcript file
  was located on disk and read directly — 1774 total lines, 155 entries
  timestamped after `13:51:26` local (36 user, 63 assistant, 37 attachment,
  16 queue-operation, 3 system), ending `15:19:59` local, proving the
  "lost" conversation was fully intact and just unread by the restore path.

## 2026-09-24 — a Claude live input that never reached the CLI

RTS 13:20 UTC, Work project chat: the server pasted a three-line message into
Claude's tmux pane, but Claude never submitted it. The text vanished and the
composer was left holding two blank lines. The draft check reads only the `❯`
line, so it saw nothing; the durable watch correctly marked the message failed
60 s later (red "!"). The re-send was recorded with two leading newlines.

Fixes:
- **multi-llm 89a7c49:** when the submit check runs out of retries, Claude's
  transcript decides. If there is no user or queue row for the send within
  6 s, the composer is emptied and the message sent once more in the same
  input slot. Blank lines are also cleared before every paste.
- **Frontend:** a failed message now shows "Not delivered · Resend" next to
  it; Resend sends the same text through the chat's normal send path.

## Register notes

[PLAT-178](plat-178.md) owns durable live-input
message persistence and native-transcript recovery when the chat UI falls behind
the coding terminal. Its 2026-09-21 follow-up also covers rapid-message
visibility: RTS proved that busy Cursor submissions may spend 15–19 seconds
waiting for a safe composer before truthful `sent_to_cli` acceptance. Formatted
Chat now stages every pending user row before its serialized delivery lane,
while the single and double delivery ticks remain governed by the durable-ack
contract; no premature server success is reported. Deployment and live Cursor
multi-send verification remain pending. [PLAT-323](plat-323.md) owns
workflow-local, per-user Builder chat storage, access isolation and the one-time
legacy migration. Together they define the Chat Reliability category; purely
visual chat defects remain under frontend-chat.
[PLAT-324](../../crew/chat-reliability/plat-324.md) owns Work/Crew tab
continuity across browser reloads, backend restarts and deployments: an
already-open tab must rebind its saved application session before its next
message can reach a provider conversation. Its latest deployed correction also
prevents a stale second hydration from removing a Cursor final that was already
visible in the live chat timeline. The 2026-09-21 follow-up `15ec6141a` removes
the frontend's completion-time provider-native transcript merge: formatted Chat
now consumes only structured events and persisted AgentWorks history, while
tmux remains the CLI process host/raw Terminal and backend adapters alone may
normalize native recovery into structured events. Focused frontend and retained
turn contract tests pass; deployment and live acceptance remain pending.
Additional 2026-09-17 multi-user/tab isolation, durable acceptance and recovery
hardening, cold-workflow recovery, duplicate snapshot/notification fixes and
shared queue ownership for decision/report buttons are deployed to RTS in
`eb93a59-20260917081444`, with health verified;
the user live continuity acceptance matrix remains pending. See the ticket for current status.
