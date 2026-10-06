[← platform / coding-agent-bridge](index.md)

# PLAT-020 — converted scheduled chat must retain its session and tmux

| Field | Value |
|---|---|
| State | deployed |
| Priority | P0 |
| Product | coding-agents |
| Area | bridge |
| Summary | workflow chat/session handoff |

| Coordination | Value |
|---|---|
| Assigned agent | `Codex` |
| Ticket state | `implementation_complete_runtime_reverify` |
| Last synchronized | `2026-08-15` |

> Claim this ticket in this file before implementation. During active work,
> update this fragment rather than the shared index; synchronize the index
> once at handoff, review, or completion.


- **Priority:** P0
- **Owner:** workflow chat/session handoff
- **Source run:** RTS Latency
  `schedule-cron--42eca39a_1785810615371091000`
- **Problem:** the earlier proposed repair rotated the tab to a new UUID. That
  was the wrong abstraction: **Make interactive** means continue this exact
  conversation, not fork it. A new session bypasses the retained tmux and loses
  the provider-native conversation identity that already carries the context.
- **Implementation (corrected 2026-08-06):** conversion preserves the session
  ID and records an explicit durable `user_interactive_continuation` marker on
  the tab. Every later query carries that marker. The backend lets this explicit
  user promotion outrank the historical `schedule-*` ID, so a replacement CLI
  resumes the same native conversation and receives normal interactive
  retention. The ordinary query path still prefers direct delivery when the
  original tmux is live. No fork or new conversation is created.
- **Verification:** backend coverage proves a settled retained tmux accepts a
  follow-up and becomes running/live again, while a session without a live tmux
  falls through to the normal same-session resume path. Frontend regression
  tests cover scheduled and bot observations and assert that conversion keeps
  both `tabId` and `sessionId`; TypeScript compilation passes. A real converted
  schedule remains to be exercised after deployment.
- **2026-08-15 regression and root cause:** commit `d18e071e1` made scheduled
  runs first-class runtime tabs. Its 10-second reconciliation merged the
  runtime Schedule projection (`isViewOnly=true`, `isScheduledRun=true`) over
  an existing tab without respecting `userInteractiveContinuation=true`.
  Consequently, a correctly converted Chat reverted to Schedule a few seconds
  later. Live evidence showed the message itself was not lost: session
  `schedule-manual--384703db_1786780881148580000` accepted the follow-up at
  14:37:27 in 82 ms and emitted its terminal completion at 14:37:59, while the
  frontend hid the interactive continuation.
- **2026-08-15 repair:** runtime-tab reconciliation now treats the explicit
  user promotion as the higher-precedence fact. It may continue refreshing
  runtime status, but cannot rewrite the tab name, phase, or read-only flags.
  Name and metadata are committed atomically so a poll cannot expose a mixed
  Schedule/Chat state.
- **Completion follow-up:** PLAT-035 records the separate defect where a
  successfully delivered retained turn stayed `foreground_turn.busy=true`
  after its tmux had returned to an idle prompt. PLAT-020 owns conversation
  continuity; PLAT-035 owns the retained turn's stream-driven end boundary.
- **Regression tests:**
  `TestTryDeliverQueryAsLiveInputReactivatesSettledRetainedTmux` plus
  `TestCodingAgentRequestAllowsPersistentInteractive` and
  `workflowChatTabConversion.test.ts` for scheduled and bot conversations;
  `workflowRuntimeTabProjection.test.ts` now converts a real-shaped Schedule
  tab, applies multiple reconciliation ticks, and proves the same tab/session
  remains interactive. The paired control proves ordinary Schedule tabs still
  receive runtime projection updates.
- **Acceptance:** active, settled, and relaunched scheduled coding-agent chats
  keep the same logical session ID and conversation context. The first user
  message is delivered exactly once—to the existing tmux when live, otherwise
  to the resumed replacement—and remains visible in the same tab.
