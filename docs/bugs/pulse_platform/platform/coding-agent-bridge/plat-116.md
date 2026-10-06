[← platform / coding-agent-bridge](index.md)

# PLAT-116 — a completed CLI turn's own JSON completion signal doesn't reliably reach the platform; the blunt 10-minute idle timeout is the only real backstop, and it leaks the session

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | platform |
| Area | coding-agent-bridge |
| Summary | P1 — silently turns real successes into false `error` schedule |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `implemented_pending_restarted_ui_reverify` — the Codex cross-session lock inversion is fixed, and `mcpagent` now owns a provider-neutral stable turn ID plus exactly-once unified completion. IC-12 makes that lifecycle a mandatory deterministic CI and live per-provider release gate. The shared real retained-session contract passed for Codex, Claude Code, Cursor, and Pi; real concurrent Codex completion and race tests passed. A restarted AgentWorks Social Media run is the remaining production/UI reverify. |
| Last synchronized | `2026-08-20` |

- **Priority:** P1 — silently turns real successes into false `error` schedule
  runs, and permanently leaks the session/goroutine so the UI keeps showing it
  as stuck long after the platform has already given up on it.
- **Owner:** provider-native completion translation (`multi-llm-provider-go`
  interactive adapters) + canonical per-turn lifecycle ownership (`mcpagent`)
  + the bridge from canonical completion to `textChan` closing
  (`agent_go/pkg/agentwrapper/llm_agent.go`,
  `cmd/server/server.go`) + the scheduler's safety net
  (`waitForConversationTurnTree`, `cmd/server/conversation_turn_lifecycle.go`)

## 2026-08-20 Social Media recurrence: root cause confirmed

The pre-run decision drain for session
`schedule-manual--5227790a_1787198314069858000` produced a stronger trace than
the earlier incidents. Codex wrote `task_complete` at `09:37:00 IST`; the
adapter logged both `codex_task_complete_observed` and
`codex_wait_returned outcome=completed` by `09:37:02`. It then logged
`codex interactive response captured` but never logged
`codex_stream_closing`. AgentWorks consequently showed no formatted final
answer and the scheduler waited until `09:47:01`, falsely timing out a turn
whose final answer had already been visible in tmux for ten minutes.

The blocking line is after provider completion, in final-answer rollout
resolution. A live Codex turn holds its own `codexInteractiveSession.mu` for
the complete GenerateContent call. `resolveCodexRolloutPathLocked` then called
`boundCodexRolloutPaths`, which locked every other live session's same mutex.
When two Codex sessions completed concurrently, session A held A's mutex and
waited for B while B held B's mutex and waited for A. The earlier claim that
skipping the caller made this safe was incorrect; it prevented self-deadlock
but not cross-session lock inversion.

The fix separates transcript identity (`rolloutPath` and `threadID`) behind a
short-lived `rolloutMu`. Rollout exclusion scans no longer touch another
turn's whole-lifetime mutex, and resolution never holds one rollout lock while
reading another. A deterministic two-session regression test holds both turn
mutexes and proves both final-resolution calls return. This is the shared
completion closure that restores formatted final output and lets the scheduler
advance without the ten-minute safety timeout.

The prior P0 coverage missed this because it exercised one retained session at
a time. Single-session completion cannot create the A-waits-for-B / B-waits-for-A
cycle. The new regression deliberately holds two sessions' turn mutexes before
resolving either rollout, so the exact concurrency boundary is now covered.

## Required platform-level closure — provider adapters translate; mcpagent owns the turn

The rollout-lock change is necessary because the reproduced deadlock lives in
the Codex adapter, but it is not the architectural closure for PLAT-116. Every
coding CLI must expose one canonical lifecycle to AgentWorks regardless of how
its native transport signals completion. Provider adapters may translate
Codex `task_complete`, Claude `stop_reason=end_turn`, Pi `agent_settled`, or a
Cursor structured result; they must not define different outward completion
semantics.

The target contract is:

1. `mcpagent` creates one stable `turn_id` for every submitted message. Session
   lifetime and turn lifetime are separate: a retained tmux/API session may
   remain alive after the current turn is complete.
2. Every canonical event carries that `turn_id`: assistant deltas, tool
   receipts, and the one `unified_completion` terminal event. That terminal
   event carries the final answer as well as completed/error status; the
   platform does not publish a second competing final/completion event.
3. The provider adapter reports its native facts; `mcpagent` owns the
   exactly-once terminal state transition and stream closure. AgentWorks, the
   scheduler, and the UI do not independently infer completion from tmux
   liveness, rollout mtimes, inactivity, or session status.
4. Transcript identity is bound at turn/session start. Normal completion must
   not scan or acquire a whole-turn lock from an unrelated session.
5. Optional enrichment — transcript rereads, token/cost extraction, usage, and
   diagnostics — runs after or independently of terminal delivery and cannot
   delay `unified_completion`.
6. AgentWorks derives formatted final visibility and busy=false from the same
   canonical terminal event; the scheduler advances from that event; the
   retained session remains reusable for the next turn.

### Shared P0 conformance suite

Run the same assertions through the real `mcpagent → AgentWorks` boundary for
Codex, Claude Code, Pi, and Cursor rather than giving each adapter a different
definition of success:

- two same-provider sessions can complete concurrently;
- a workflow schedule and an interactive chat can complete concurrently;
- each turn exposes its final answer promptly and emits exactly one terminal
  completion;
- the formatted spinner stops and canonical busy becomes false while the
  underlying retained session stays alive and accepts a follow-up message;
- tool/final output never crosses turn or session IDs;
- a stuck enrichment or transcript-diagnostic path cannot hold completion;
- provider-native completion-to-canonical-completion latency is measured and
  fails the P0 gate instead of falling through to the scheduler's ten-minute
  timeout.

This contract now passes live for all four coding CLIs. Product closure still
requires a restarted AgentWorks run because the currently running server does
not contain the new `mcpagent` and wrapper code.

### Mandatory release enforcement

The architectural contract above is now canonical IC-12 in
`agent_go/docs/cross_repo_integration_contract.md`, not advisory prose in this
ticket. Every PR runs mcpagent's credential-free P0 lifecycle test. The live
coding-CLI P0 additionally rejects a retained turn unless its tool receipts and
single `unified_completion` carry the same stable `turn_id`, the completion is
explicitly marked canonical, the final answer is visible, and the underlying
session remains reusable. New adapters and production product profiles inherit
the same contract through mcpagent rather than defining provider-specific
completion behavior.

## Why this ticket exists

A live incident on 2026-08-16: the `upgrade-periodic-pulse-review` contract
upgrade turn (the new rollout rung from [PLAT-115](../../goals/pulse-governance/plat-115.md))) ran against
social-media as schedule `5227790a-...`, session
`schedule-manual--5227790a_1786887536591212000`. The scheduler's own history
records it as `error`:

```
workshop turn 1/2 (upgrade-periodic-pulse-review) failed: workshop idle wait
timed out: execution query_1786887536605000000 made no progress for 10m0s
(running_children=0)
```

`started_at=13:38:56Z`, `completed_at=13:58:09Z` — a 19-minute run that ends
in a declared failure.

That failure is wrong. The turn actually succeeded:

- **Codex's own rollout JSONL proves it.** The candidate file resolved by
  working directory + tmux session name + timing
  (`~/.codex/sessions/2026/08/16/rollout-2026-08-16T19-09-01-...jsonl`)
  contains `event_msg`/`task_complete` at `13:40:56.495Z` — **two minutes**
  after the turn began, not ten — with `last_agent_message` reading
  *"Completed the migration and stamped workflow contract version
  1.0.26. Kept per-run monitoring..."*
- **The side effect it describes actually landed.** `workflow.json` on disk
  really does show `version: 1.0.26` — the migration genuinely ran and
  persisted, unaffected by what happened next.
- **The terminal really did go idle right after.** A UI long-poll of the tmux
  pane at `13:42:37Z→13:48:09Z` (`19:12:37→19:18:09` IST) returned 0 bytes
  changed over a 2m22s window — nothing was happening in that pane.

So the CLI-level, JSON-based completion signal this ticket's reporter asked
about by name — *"we have tmux streaming we have proper final message
response via codex json"* — exists, is real, and fired correctly. The gap is
entirely on the platform side: **something between that signal and the
platform's own turn-completion state never closes the loop**, so the only
thing that ever resolved this turn was the scheduler's blunt 10-minute
inactivity timeout (`waitForConversationTurnTree`,
`conversation_turn_lifecycle.go:301-307`) — a safety net never meant to be
the primary signal, doing exactly what a safety net does when the real
signal is silent: it waits, then declares failure, even though the work
underneath it had already succeeded 17+ minutes earlier.

## Traced call chain (confirmed by log evidence, not assumption)

```
scheduler.go: runPostRunMonitor → workshop turn (schedule-triggered chat query)
  → server.go:6272 "llmAgent.StreamWithEvents() started successfully"
  → server.go:6277 "Entering streaming loop" → for chunk := range textChan   [BLOCKS HERE]
      (agentwrapper/llm_agent.go:1069 StreamWithEvents — textChan only closes
       when the goroutine below returns)
  → mcpagent Session.Run (agent/turn_session.go:187)
  → continueAgentSessionWithHistory (agent/session_handle.go:124, native-resume path)
  → askWithHistory → GenerateContentWithRetry → llmInstance.GenerateContent
  → multi-llm-provider-go codexcli_adapter.go → generateContentInteractive
  → waitForCodexInteractiveResponse (codexcli_interactive_adapter.go:1814)
      — this loop DOES prioritize completionTracker.completed() over terminal
        text (lines 1897-1908) and, per the rollout evidence above, the event
        it is watching for really was written at 13:40:56Z.
```

No `[STREAMING_LIFECYCLE] StreamWithEvents completed` line, no
`[COMPLETION] Updating session ... completed`, no `[AGENT DEBUG] Query ...
completed successfully` — none of `server.go`'s own completion logging ever
fired for this session between the turn starting and the scheduler's timeout
19 minutes later. The blocking `for chunk := range textChan` at
`server.go:6278` genuinely never unblocked; the scheduler's 10-minute
watchdog is a completely separate mechanism (`LastProgressAt` on the tracked
execution tree) and has no way to reach into, or resolve, that stuck
goroutine — it can only give up waiting on it.

## Most likely mechanism (narrowed, not proven)

`codexTurnCompletionTracker.completed()`
(`codexcli_transcript_completion.go:65-105`) is the exact signal PLAT-108
already bound to session identity for the Codex interactive transport. Its
resolver, `codexRolloutResolverForSession`
(`codexcli_rollout_binding.go:111-132`), snapshots `threadID` /
`rolloutPath` / the set of rollouts other sessions have claimed **once**,
into a closure reused for the entire poll loop. For a turn whose rollout
file is brand new (this one was created ~5 seconds after the turn began, not
a pre-existing pinned thread), that closure falls through every single poll
to a live `findCodexRolloutByWorkingDirExcluding` directory scan.

Checked and ruled out one candidate explanation: the rollout's own
`session_meta.cwd` is `/Users/mipl/ai-work/mcp-agent-builder-go/workspace-docs/Workflow/social-media`
— it exactly matches the workflow's real working directory, so a naive
working-directory mismatch is not the cause. The remaining gap is inside
that live directory-scan/exclusion path or the thread-ID snapshot timing,
and pinning it further needs either debug instrumentation on a real
reproduction or careful review of `codexPersistentRegistry` state at the
exact moment this session's tracker snapshot was taken — deliberately not
pursued further in this pass (see "Deliberately deferred" below).

## This is not Codex-specific

The reporter's correction mid-investigation — *"also this should work for
all, not just codex"* — is the right framing and matches how PLAT-108
already treated this as a class, not a one-off. The vulnerable layer above
the provider-specific tracker is entirely provider-agnostic:

- `server.go`'s `for chunk := range textChan` has no timeout of its own; it
  blocks on whatever the provider layer eventually does.
- `mcpagent`'s `Session.Run` → `askWithHistory` → `GenerateContent` chain is
  identical for every CLI provider.
- The scheduler's `waitForConversationTurnTree` 10-minute safety net is
  provider-agnostic by construction — it has no idea which provider is
  running underneath it, which is exactly why it can only ever produce a
  generic "made no progress" failure instead of a real diagnosis.

## 2026-08-19 Build-in-Public recurrence: completion miss reproduced; next-turn failure is separate

Build-in-Public Pulse-only session
`schedule-cron--af5a941f_1787078711034693000` reproduced this ticket on the
current binary. Gate durably wrote its worklist and the bound Codex rollout
reached terminal completion, but AgentWorks did not observe it. At 00:25:14 IST
the idle-timeout path marked the session `error` and ran `CloseHTTPSession`.

That evidence keeps the deeper completion-bridge root open: diagnostic cleanup
prevents one kind of leaked activity, but it does not make provider completion
reach the canonical turn lifecycle.

The scheduler then attempted Review+Fix in the same continuing Pulse
conversation and hung because it reused the session that cleanup had just
closed. That second boundary is tracked separately as
[PLAT-148](../../goals/pulse-governance/plat-148.md)). PLAT-116 owns why Gate required orphan cleanup;
PLAT-148 owns why cleanup destroyed the conversation needed by the next
sequence message. They require coordinated P0 coverage but must not be
collapsed into one ambiguous fix.

### Focused completion tracing added for the next reproduction

The previous logs stopped at AgentWorks entering `for chunk := range textChan`,
so they could prove the bridge was stuck but could not identify which boundary
lost completion. Focused, content-free `[COMPLETION_TRACE]` checkpoints now
record the correlation chain needed to pin the root cause on the next live run:

- AgentWorks session/query begins waiting on the stream, and records when that
  stream actually closes;
- mcpagent records when its provider call starts and returns, including the
  provider-native session ID;
- the Codex adapter records the owner session, tmux session, bound Codex thread
  and exact rollout path, then the native turn ID and offset where
  `task_complete` was observed;
- the adapter records its wait return and its stream-channel close.

No prompt, response, tool arguments, or tool output is included. This is
diagnostic instrumentation only: it deliberately does not change completion
semantics or mark PLAT-116 resolved.

Checked each interactive-capable adapter directly rather than assuming
parity from the provider contract table. First pass wrongly claimed Claude
Code has no JSON completion signal — corrected after review found
`waitForCompletedAssistantResponseFromTranscript`
(`claudecode_interactive_adapter.go:534`), which reads Claude's own JSONL
transcript for an assistant message committed with `stop_reason=end_turn`
and treats it as authoritative: it *overrides* whatever text the pane-scrape
found, and — stricter than Codex's version — explicitly fails the turn if
the transcript exists but shows no completed `end_turn`, rather than
silently trusting a possibly-stale pane scrape.

| Provider | Native JSON/transcript completion signal in interactive (tmux) mode? |
|---|---|
| Codex CLI | **Yes** — `task_complete` in the rollout JSONL, tracked as the primary signal inside the poll loop (`codexTurnCompletionTracker`, this ticket's reproduced incident) |
| Claude Code | **Yes** — `stop_reason=end_turn` in Claude's own JSONL transcript (`completedAssistantResponseFromTranscript`), read once the pane looks idle and treated as authoritative over the pane text; can fail the turn outright if the transcript disagrees |
| Pi CLI | **Yes** — an `agent_end` marker from Pi's own embedded JS harness (`picli_interactive_adapter.go:1473-1477`) is the *sole* completion signal for the wait loop — no terminal-text fallback at all |
| Cursor CLI | **No** in interactive/tmux mode — pure terminal-pane pattern matching (`waitForCursorInteractiveResponse`). Cursor's *default* transport is structured (one-shot `--json`), which sidesteps this whole class of problem by design; interactive/tmux mode is only reachable via an explicit per-profile transport override, so this is not a live exposure today |

So all three tmux-interactive providers actually in use (Codex, Claude Code,
Pi CLI) have a genuine JSON/transcript-based completion oracle, each bound
to session identity per PLAT-108. The exposure this ticket is about is
therefore fully shared, not Codex-specific: the shared bridge
(`textChan`/`Session.Run`) and the scheduler's blunt 10-minute safety net sit
*above* all three signals equally, provider-agnostic by construction — so
whatever the exact Codex-side miss turns out to be once traced further, the
same shared-layer gap can just as easily swallow Claude Code's `end_turn` or
Pi's `agent_end`. Cursor is the one provider genuinely unaffected today,
because its default transport never enters this code path at all.

This ticket's scope is that shared layer plus, for Codex specifically, the
one concretely reproduced tracker-miss.

## Consequence found live: the session leaks and never gets cleaned up

Checked directly while this incident was still visibly "stuck" in the UI,
after the scheduler had already recorded the terminal `error` result:

- `[ACTIVE_SESSION] Tracked active session: schedule-manual--5227790a_...`
  was logged exactly once, at turn start. No matching untrack ever appears.
- None of `CloseHTTPSession`, `removeSessionQueryID`, or
  `cleanupBrowserSessions` — all three of which server.go's own completion
  path (`server.go:4102-4110`, `~6600-6620`) runs on every real completion —
  ever ran for this session.
- The underlying tmux pane (`mlp-codex-cli-int-1786887540197594000-1f06f68e`)
  was independently confirmed gone (`tmux capture-pane` returns "can't find
  pane") several minutes after the scheduler's timeout fired, while the
  session was still being served as "active" to the UI.

This is the exact symptom the reporter flagged live: *"the schedule is still
stuck till now"*, observed several minutes after the backend had already
moved on. The scheduler's timeout only unblocks *its own* wait — it cannot
reach into and terminate the separate goroutine still parked on `for chunk
:= range textChan`, since that goroutine owns the session's active-tracking
state and is the only code path that clears it. The two problems compound:
not only is a successful turn recorded as a failure, the session it ran in
is never released, so it keeps presenting as "running" indefinitely — a
resource leak on every occurrence, not just a UI staleness artifact.

## What shipped: reuse the same reliable signals as a safety-net corroboration + leak fix

Both providers with a real completion signal reachable outside their live
session registry (Codex, Claude Code) now get a second, independent use of
that same signal, purely as a post-hoc diagnostic — never as a live
completion oracle, so this can never make a stuck turn falsely report
success:

- **`multi-llm-provider-go`** — two new exported, read-only functions, each
  a thin wrapper around the exact reader the interactive adapter already
  trusts as authoritative:
  - `codexcli.DiagnoseTurnCompletion(workingDir, since)` — scans the rollout
    for a `task_complete` event after `since` (`codexcli_turn_diagnostics.go`).
  - `claudecode.DiagnoseTurnCompletion(nativeSessionID, workingDir, since)` —
    wraps `completedAssistantResponseFromTranscript` for a committed
    `end_turn` after `since` (`claudecode_turn_diagnostics.go`).
  - Deliberately **not done for Pi CLI**: its marker file lives in a
    per-session temp directory only known to its internal live-session
    registry, with no OS-standard path an external caller can scan the way
    Codex's `~/.codex/sessions/...` or Claude Code's `~/.claude/projects/...`
    allow. Doing this for Pi needs exporting registry-level lookup first — a
    real, separate piece of work, not done here.
  - Also **not done for Cursor CLI**: confirmed not reachable in its default
    (structured) transport, so there is nothing live to corroborate today.

- **`agent_go`** — `cmd/server/conversation_turn_stall_diagnostics.go`, wired
  into `waitForConversationTurnTree`'s idle-timeout branch
  (`conversation_turn_lifecycle.go:301-308`). When the 10-minute safety net
  gives up on a turn, two things now happen instead of one:
  1. **Diagnosis**: `diagnoseStalledConversationTurn` looks up the session's
     live `*mcpagent.Agent` (already registered in `api.runningAgents` at
     turn start, so reachable even while the turn is stuck), snapshots its
     provider/working-dir/native-session-id via the already-exported
     `mcpagent.SnapshotAgentSession`, and dispatches to the right adapter's
     `DiagnoseTurnCompletion`. A hit enriches the timeout error with exactly
     what the CLI actually said and when — turning a bare "made no progress"
     into an immediately diagnosable "Codex/Claude Code completed at HH:MM:SS
     with: '...' — the platform's bridge never observed it (PLAT-116)",
     instead of requiring the manual rollout-file archaeology this ticket
     itself needed.
  2. **Cleanup, unconditionally**: `cleanupOrphanedConversationTurnSession`
     runs the same teardown the normal completion path runs —
     `updateSessionStatus(..., "error")`, `removeSessionQueryID`,
     `mcpagent.CloseHTTPSession`, `cleanupBrowserSessions` — regardless of
     whether the diagnosis found anything. This is what actually fixes the
     live "the schedule is still stuck till now" symptom: the session stops
     presenting as `"running"` in the active-session map the moment the
     safety net gives up, instead of leaking until the server process
     restarts. Every step is idempotent, so nothing breaks if the originally
     stuck goroutine ever does unblock and runs its own cleanup afterward.

## Formalized as a required P0 contract, not left as two one-off adapters

Raised directly: two adapters happening to have `DiagnoseTurnCompletion`
because I wrote them proves nothing about the *next* adapter, or about Pi
CLI staying without one indefinitely. `multi-llm-provider-go` already has a
mature capability-flag → required-certification framework
(`coding_agent_contract.go` + `coding_agent_certification.go`) — exactly the
mechanism PLAT-108's own unfinished "Layer 3 — certify it" plan called for
generally. Extended it rather than inventing something new:

- **`CodingAgentProviderContract.SupportsStalledTurnDiagnosis`** — a new
  capability flag, `true` only for `ProviderClaudeCode` and `ProviderCodexCLI`
  today (Pi CLI and Cursor's rationale for staying `false` is documented
  inline, same as the "not Codex-specific" section above).
- **`CertStalledTurnDiagnosis`** — a new certification ID, added to the
  `codingAgentCapabilityCertifications` gating table (so
  `TestAllCodingAgentCapabilityClaimsHaveRegisteredCertification` enforces
  every provider claiming the flag has a registered proof) and promoted to
  P0 wherever claimed, in both `CodingAgentCertificationPriorityForID` and
  `RequiredP0CodingAgentCertificationIDs` — the identical pattern already
  used for `CertStructuredStreaming`. A false "made no progress" timeout on
  a turn that actually succeeded is release-blocking, not a nice-to-have.
- **Real live E2E certs, not synthetic fixtures** —
  `codexcli_stalled_turn_diagnosis_live_test.go` and
  `claudecode_stalled_turn_diagnosis_live_test.go`: each runs one genuine
  CLI turn to completion (real tmux, real API), then calls
  `DiagnoseTurnCompletion` with **no turn in flight** and proves it
  independently rediscovers that exact turn's real completion text and
  timing. This is a materially different proof than the deterministic
  fixture tests already covering the parsing logic — it certifies the
  function against a genuine provider transcript, not a hand-written one.
  **Actually run live** (`-coding-cli-p0-live`) against real `codex` and
  `claude` CLIs on this machine, both passed (10.3s and 8.3s), both tmux
  sessions confirmed cleanly torn down afterward — not just written and
  assumed, per this codebase's own standing rule that this class of bug has
  repeatedly passed unit suites while broken.
- Every existing contract/certification drift test reverified passing
  after the change, including the strictest one
  (`TestActiveCodingAgentProvidersSatisfyP0Contract`, which independently
  checks `Priority == P0`, `RealE2E == true`, and the exact live-gate `Env`
  for every ID `RequiredP0CodingAgentCertificationIDs` returns).

## Confirmed live, same day: the bug recurs, and the first cleanup pass was incomplete

A real social-media schedule run reproduced this exact class of bug the same
day the fix shipped — with a materially worse consequence than the original
report: the underlying execution had **genuinely succeeded** (a real post
landed and was independently verified — `task_complete` at 19:20:44 UTC:
*"Done — the pinned execution route ran exclusively... One original post
landed and was verified..."*), but the platform's bridge never observed that
completion, the scheduler's idle-wait declared it failed 12 minutes later,
and Pulse's "workflow did not start" fallback path produced a **false**
summary claiming nothing happened — directly contradicting the real,
verified evidence sitting in the same rollout.

**The diagnostic itself didn't help here**, and the reason is instructive:
`codexcli.DiagnoseTurnCompletion` locates a rollout by "newest file matching
this working directory" — deliberately accepted as a known-imprecise
fallback in the original design (see "Most likely mechanism" above). This
run had `running_children=2` — other Codex sessions genuinely active in the
same social-media working directory at the same moment — so the lookup most
likely grabbed a *different* concurrent session's rollout instead of the
root turn's own. This is exactly the ambiguity PLAT-108 already named and
warned would keep recurring wherever a lookup uses proximity instead of
identity; it is not fixed by this ticket.

**The cleanup half had a real, separate gap, found by checking the live UI
directly.** The session's `status` was correctly set to `"error"` (confirmed
in `server_debug.log`), but Global Monitor and the workflow selector
(`ModePresetBar.tsx`, `currentSessionTone`) kept showing the workflow as
actively running for hours afterward. Neither surface actually reads session
`status` for this — they read `api.sessionBusy[sessionID]` and the
`TrackedWorkflowExecution` record for the root query, and the first version
of `cleanupOrphanedConversationTurnSession` cleared neither. `sessionBusy` is
only ever cleared by the normal completion path (`server.go:4236`), which —
being the exact path permanently blocked on `for chunk := range textChan` —
never ran. Fixed: the cleanup now also calls `setSessionBusy(sessionID,
false)` and `completeTrackedExecution(rootExecutionID,
trackedExecutionStatusFailed, ...)`, both idempotent alongside the rest of
the function. Verified failing before the fix (both UI-relevant test
assertions fail with the sessionBusy/tracked-execution lines reverted) and
passing after.

**Incidental finding, not fixed here**: while tracing this, PLAT-114's
durable `background_agent_log` was checked directly and showed two rows for
this same run permanently stuck at `status='running', completed_at=NULL`
(`workflow-full-msvyee8q01-step-0-msvyijui03`,
`-step-0-msw3jq3i0f`), even though `server_debug.log` explicitly records
them being settled: *"Settled 2 orphaned progress child(ren) of finished
execution..."* (`delegation.go`'s `ReconcileOrphanedProgressChildren`,
PLAT-091). That function correctly transitions the *live* in-memory
`BackgroundAgent` to `BGAgentFailed` with a real `CompletedAt` via
`agent.SetError` — confirmed this is NOT the cause of the live "still
running" symptom above, since `HasRunningAgents`'s grace period had long
expired by the time this was checked. But it calls `SetError` directly
instead of `emitBackgroundAgentCompleted`, so it never reaches PLAT-114's
durable-log completion hook. A background agent settled this way stays
`running` in the durable log forever, which would read as a real anomaly to
anyone (or any future agent) auditing that table directly. Not fixed in this
pass — flagged here since PLAT-114 owns that log's completeness.

## Formerly deferred bridge root cause is now pinned; shared closure remains

The earlier investigation left the bridge stall unexplained. The 2026-08-20
trace and concurrent-session review supersede that deferral for the reproduced
Codex path: final-answer rollout resolution blocked on another live session's
whole-turn mutex after native completion. The dedicated rollout-identity lock
fixes that mechanism. What remains deliberately open is not the Codex RCA, but
the provider-neutral lifecycle ownership and conformance work specified above.

## Verification

- `mcpagent` unit and race tests prove one stable `turn_id`, duplicate terminal
  suppression, canonical error completion, concurrent turn isolation, and
  retained direct-delivery completion.
- The real retained-session P0 ran through `mcpagent.Session.Run` for **Codex,
  Claude Code, Cursor, and Pi**. Each provider completed two sequential turns,
  reused the same live coding-CLI session, returned the expected final answer,
  and emitted exactly one matching canonical `unified_completion` per turn.
- The real concurrent Codex P0 ran two live sessions together and passed with
  isolated tool/final output and exactly one canonical completion per turn.
- The deterministic Codex lock regression and its race run pass: both sessions
  hold their full-turn mutexes while resolving their bound rollouts, and both
  return without cross-session lock inversion.
- `go build ./...` passes in `mcpagent` and AgentWorks. The AgentWorks wrapper
  suite and race suite pass. Full `mcpagent/agent/...` has no functional
  regression; its only failure is the existing manual approval gate for four
  previously captured streaming-review JSON records.
- Focused AgentWorks server completion/retained-session consumer tests pass,
  including the documented unified-completion final-answer extraction path.

- `codexcli_turn_diagnostics_test.go` / `claudecode_turn_diagnostics_test.go`
  (`multi-llm-provider-go`): finds a completion after `since`, ignores one
  before it, and reports not-found when no transcript/rollout exists at all.
- `conversation_turn_stall_diagnostics_test.go` (`agent_go`):
  `TestWaitForConversationTurnTreeIdleTimeoutRunsOrphanCleanup` — the direct
  regression guard, proving the idle-timeout path now leaves the session's
  active-tracking status as `"error"` and clears its query index, not stuck
  at `"running"` forever. **Verified failing before the wiring change and
  passing after** (temporarily reverted the one-line wiring, confirmed the
  test fails with the exact live symptom — status stays `"running"` —
  restored it, confirmed the test passes). Cleanup idempotency and the
  no-running-agent early-return are covered separately.
- **Follow-up (same day)**: both `TestCleanupOrphanedConversationTurnSessionClearsActiveSessionAndQueryIndex`
  and `TestWaitForConversationTurnTreeIdleTimeoutRunsOrphanCleanup` extended
  to also assert `sessionBusy` clears and the `TrackedWorkflowExecution`
  reaches `trackedExecutionStatusFailed` — the two fields the workflow
  selector and Global Monitor actually key off. Fail-before/pass-after
  reverified the same way as the original fix.
- **Now live-reverified**, not just unit-tested: this exact class of bug
  reproduced on a real social-media schedule run the same day, independent
  of any test — traced end-to-end through real rollout evidence
  (`~/.codex/sessions/...`), `server_debug.log`, and PLAT-114's durable
  background-agent log, confirming both the false-failure report and the
  stuck-spinner UI symptom this ticket describes.
- Full existing suite reverified after the change: the only failures present
  (23, across `cmd/server/guidance` Pulse/prompt-content tests and one
  unrelated `schedule_execution_history_test.go` pair) are pre-existing and
  untouched by any file this change modified — zero new failures introduced.
  fix reads from.

## Pi CLI structured completion: two theories, one wrong, one real (2026-08-18/19)

An earlier version of this section claimed Pi's structured adapter had a
terminal-event bug and that it was fixed. **Both claims were wrong.** The
section is rewritten here rather than deleted, because the false claim was
acted on twice and caused a production regression, and because the way it was
reached is the actually-useful lesson.

### The claim that was wrong

> *"pi 0.84.2 does not emit `agent_settled` at all — measured over a full day
> of production logs, 0 occurrences against 57 `agent_end`."*

`agent_settled` **is** emitted by pi 0.84.2, as the final event of every run.
The "0 occurrences" was a **logging artifact created by the measurement
itself**: the structured adapter *handled* `agent_settled` in its own `case`
arm, which logs nothing, while `agent_end` fell through to the `default:` arm,
whose only job is to `Debugf("pi: unhandled event type=%q")`. Grepping the logs
therefore compared a deliberately-silent handled event against a deliberately-
logged unhandled one, and read the silence as absence.

### Verified directly against the real CLI

Run with a real Gemini key, a real stdio MCP server configured through
`.pi/mcp.json`, and the exact flags the adapter builds
(`--print --mode json --no-builtin-tools -e npm:pi-mcp-adapter --approve`):

```
=== event types seen ===
   1 "type":"agent_start"    1 "type":"turn_start"
   1 "type":"turn_end"       1 "type":"agent_end"
   1 "type":"agent_settled"          <-- emitted, and last
>>> pi EXITED on its own after ~5s
```

`agent_settled` fires, it is the terminal event, and pi exits on its own
afterwards **with the MCP extension loaded and an MCP server running**. No
stale process, no held stdout, no hang.

### Why `agent_settled` is the correct signal (and `agent_end` is not)

From pi's own source (`dist/core/agent-session.js`):

```js
async _runAgentPrompt(messages) {
    this._isAgentRunActive = true;
    try {
        await this.agent.prompt(messages);
        while (await this._handlePostAgentRun()) {
            await this.agent.continue();   // tool loops + auto-retries run HERE
        }
    } finally {
        await this._emitAgentSettled();    // once, guaranteed, after the loop
    }
}
```

- `agent_settled` is emitted from a **`finally` block** — it cannot be skipped
  on success, error, or abort — **once per run**, *after* the
  `while (_handlePostAgentRun())` loop that drives multi-step tool use and
  retries has fully drained. It also flips `_isAgentRunActive = false` and
  resolves the internal idle-wait promise; it is the same signal pi's own
  `waitForIdle()` is built on.
- `agent_end` fires **inside** that loop — once per agent pass, many times per
  run.

So the original adapter (`case "agent_settled"`) was correct by construction.
Swapping in `agent_end` replaced a guaranteed-once terminal event with one
guaranteed to fire repeatedly *mid-run*.

### What that mistake cost

`multi-llm-provider-go@6609765` added `agent_end` as a teardown trigger. Within
hours it killed a healthy, still-working pi process — its 3s natural-exit grace
expired while pi was legitimately mid-run:

```
11:56:27 [SHUTDOWN] natural-exit grace (3s) expired for pid 67763
11:56:27 [SHUTDOWN] SIGTERM attempt 1/3 to pgrp 67763
11:56:27 [SHUTDOWN] process 67763 exited after SIGTERM #1
```

surfacing to the user as `pi run failed: exit status 143` (pi's print-mode
SIGTERM handler is literally `process.exit(143)`). Reverted in
`multi-llm-provider-go@fd00585`. A second follow-up attempt — reworking all
four structured adapters to drive completion from a concurrent `cmd.Wait()`
instead of a parsed event, on a theory that a child process inherits and holds
pi's **stdout** — was **abandoned before commit** once that specific premise
collapsed. That theory was independently disproven too: the MCP SDK spawns
stdio servers with `stdio: ['pipe','pipe', stderr]`, so an MCP child gets its
**own** pipes and never inherits the parent's stdout. Commit `da13e17`, cited
in earlier versions of this note, is a dangling rebase object reachable from
no branch.

### The lesson worth keeping

Absence of a log line is not absence of an event — especially when the code
being investigated is what decides whether that event gets logged. Two live CLI
runs (~2 minutes) would have refuted the claim before any code changed; instead
it was defended through three successive rounds of source-reading and theory.

### What actually explained it: stderr, not stdout

Abandoning the stdout theory did not mean the incident was fictional — the
65-minute hang was real, on both the original occurrence and a second one the
next day. **[PLAT-139](plat-139.md)** found the real mechanism, live, via a
production goroutine dump: `cmd.Wait()` itself was blocked, not on stdout (it
had already closed cleanly) but on `os/exec`'s own internal stderr-copy
goroutine — the adapter used `cmd.Stderr = &bytes.Buffer{}`, which hands
`os/exec` a pipe the adapter never had a handle to close. Fixed via
`cmd.StderrPipe()`, symmetric to stdout, in
`multi-llm-provider-go@6d8e4e9`. Full account, including the hermetic
reproduction whose pre-fix failure produces the identical stack trace as the
production incident, is in [PLAT-139](plat-139.md).

The stdout theory's own abandonment is what made room to find this: it was
wrong specifically about *which pipe*, not about there being a pipe held open
past the process's own exit. The two follow-on lessons from this ticket's
history compound cleanly — verify against the real CLI before theorizing
further (this section), and when a theory is disproven, keep looking rather
than concluding the underlying incident was never real (the next section).
