[← goals / pulse-governance](index.md)

# PLAT-196 — `required_pulse_review_modules:completion-check-session-id-resolution`: root cause not confirmed, diagnostic logging added to catch the next recurrence live

| Field | Value |
|---|---|
| State | open |
| Priority | P2 |
| Product | goals |
| Area | pulse-governance |
| Summary | P2 — a background Pulse review can be reported incomplete |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `open` — investigated at length, no confirmed root cause, no fix; diagnostic logging shipped so the next occurrence carries evidence. See the 2026-09-03 update: much more frequent than scoped, but no occurrence since the diagnostics shipped |
| Last synchronized | `2026-09-03` |

- **Priority:** P2 — a background Pulse review can be reported incomplete
  (blocking the workflow) even when the review receipt is durably present,
  or the reverse could theoretically also be true (a receipt silently
  attributed to the wrong run). Confidence in the failure mechanism is
  currently low, so priority reflects the blast radius of the *symptom*,
  not a confirmed defect.
- **Owner:** N/A yet — no fix landed. Candidate files once a mechanism is
  confirmed: `agent_go/cmd/server/pulse_runtime_guard.go`
  (`pulseRunIDForSession`), `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/interactive_workshop_manager.go`
  (`requireBackgroundPulseReviewReceipts`, `runBackgroundTaskAgentSequence`),
  possibly `mcpagent/executor` (session-id context propagation for native,
  non-shell-bridge tool calls).
- **Related:** `harness:required_pulse_review_modules:completion-check-session-id-resolution`
  (confida-login), filed 2026-08-25: *"Harness reported this pulse_run_id's
  technical_review reviewer child as status=failed (\"required
  technical_review receipt for child session
  workshop-background-task-1787636631348580000 was not recorded: sql: no
  rows in result set\") even though the receipt IS durably present."* Same
  investigative family as PLAT-191/PLAT-195 (a stale/misdiagnosed harness
  finding), but here the investigation did **not** reach the same clean
  "already fixed" conclusion — it reached a genuine unresolved unknown.

## What was traced, and what it ruled out

1. **Receipt write path.** `complete_pulse_review` (`pulse_worklist.go`) →
   `pulseRunIDForSession(ctx, pulseRunID)` → `step_based_workflow.CompletePulseReview`
   → `pulse_review_log` table, keyed by `review_run_id` + `module`. Confirmed
   correct: the same resolved ID is used consistently as both the write key
   and the `pulse_run_id` value recorded alongside it.
2. **Receipt read path.** `requireBackgroundPulseReviewReceipts` (called from
   `runBackgroundTaskAgentSequence` after a background task agent's turns
   complete) reads `LoadPulseReviewReceiptForRun(ctx, workspacePath,
   config.MCPSessionID, module)` — i.e. it expects the review to have been
   written under exactly `config.MCPSessionID`.
3. **Bridge/shell session-scoping.** Traced whether background task agents
   might be missing `configureWorkshopToolAgentBridgeSession` — a documented,
   already-shipped fix for a *sibling* bug class (shell-originated bridge
   calls inheriting the wrong MCP session, so a nested curl gets attributed
   to the parent session instead of the child). Initially concluded
   background tasks were missing it (a grep across
   `runBackgroundTaskAgentSequence`'s body found no direct call). **This was
   wrong** — `runBackgroundTaskAgentSequence` calls
   `configureWorkshopToolAgentSession`, which transitively calls
   `configureWorkshopToolAgentSessionWithID` → `setupWorkshopToolAgentSession`
   → `configureWorkshopToolAgentBridgeSession`, one level of indirection the
   first grep missed. Background tasks **do** get correct bridge-session
   re-scoping. This hypothesis is ruled out.
4. **`"current"` resolution.** `pulseRunIDForSession` resolves the literal
   string `"current"` via `mcpexecutor.SessionIDFromContext(ctx)` — a
   context value pair (`WithSessionID`/`SessionIDFromContext`,
   `mcpagent/executor/session_context.go`). Confirmed `WithSessionID` is
   called in exactly one place in all of `mcpagent`: the HTTP
   `custom-execute` handler (`executor/handlers.go:593`), populated from the
   incoming request's `SessionID` field. That HTTP path is fed by
   `X-Session-ID`, which for the trusted session-scoped bridge route
   (`/s/{session_id}/tools/...`) comes from the URL path itself
   (`agent_go/cmd/server/server.go:1930-2017`) — this part is sound.

## The unresolved gap

`WithSessionID` is called in exactly one place, on the HTTP
(shell/bridge-originated `curl`) tool-call path. It was not possible to
confirm, from static tracing alone, how — or whether — a **native,
in-process** tool call (no HTTP round trip; the path a code-execution-mode
or directly-registered-tool call can take) gets a session id attached to its
`ctx` at all. If it doesn't, `pulse_run_id="current"` would resolve to empty
on that path; if it does via some other mechanism not yet located, the
mechanism (and whether it's guaranteed to match `config.MCPSessionID`) is
still unverified. Two considerations kept this from being pursued further by
guesswork:

- Pulse review completion clearly works correctly most of the time across
  the platform (many other PLAT tickets depend on successful receipts), so a
  universally-empty resolution on this path can't be the whole story either.
- Further static tracing without a concrete reproduction would be guessing,
  not investigating — the responsible stop here is to instrument and wait
  for live evidence rather than ship a fix aimed at an unconfirmed cause.

## What shipped instead: diagnostic logging, no behavior change

No control flow, validation, or error handling changed. Three log points
added, all `[PULSE]`-prefixed to match this codebase's existing Pulse log
convention:

1. `pulseRunIDForSession` (`pulse_runtime_guard.go`) — logs the resolved
   session id (or explicitly logs "resolved to EMPTY") every time
   `pulse_run_id="current"` is resolved via ctx. This is the single most
   direct way to observe, live, whether a reviewer's `complete_pulse_review`
   call ever resolves `"current"` to empty or to something unexpected.
2. `complete_pulse_review` handler (`pulse_worklist.go`) — logs the
   `review_run_id` actually used for the write, alongside `modules` and
   `status`, immediately before the write call.
3. `requireBackgroundPulseReviewReceipts` (`interactive_workshop_manager.go`)
   — on a missing-receipt failure, calls a new diagnostic-only read,
   `RecentPulseReviewRunIDsForModule` (`pulse_review_log.go`), and logs the
   most recent `review_run_id`s that *did* write a receipt for that module.
   If a recurrence shows a receipt landed under a different id than the one
   `requireBackgroundPulseReviewReceipts` expected, that's the smoking gun
   for a session-id mismatch; if the log shows the exact expected id present
   with a non-`completed` status, or shows no recent writes at all, that
   points to a different mechanism entirely (e.g. the reviewer turn never
   actually called `complete_pulse_review`).

`RecentPulseReviewRunIDsForModule` is read-only (`SELECT review_run_id ...
ORDER BY _id DESC LIMIT ?`) and only runs on the failure path — no added
cost on the success path, no new schema, no behavior change on any
previously-passing case.

## Explicitly not done

- No fix to `pulseRunIDForSession`, the bridge-session-scoping mechanism, or
  `requireBackgroundPulseReviewReceipts`'s matching logic — none of those are
  confirmed broken.
- Did not trace native (non-HTTP) tool-call context propagation inside
  `mcpagent`'s executor beyond confirming `WithSessionID`'s single call site
  — that trace needs either more time than this pass had budget for, or (better)
  a live log capture from the next recurrence to point directly at the
  divergence instead of guessing at it blind.

## Verification

- `go build ./...` clean (only a pre-existing unrelated onnxruntime linker
  warning).
- `go test ./pkg/orchestrator/agents/workflow/step_based_workflow/...` and
  `go test ./cmd/server/...`: same failing tests before and after this
  change (`TestRunInBackgroundPassesBuilderSkillSnapshotToBothAgentKinds`,
  `TestWorkshopPromptShellExamplesUseAbsolutePaths`,
  `TestWorkshopResolveLLMConfigExpandsCodingAgentMode`,
  `TestStandalonePulseReviewCommandsUsePersistedReviewerPipeline`,
  `TestArtifactDriftAuditsTheSchedule`), confirmed via `git stash` against
  the same baseline — no regression introduced.
- No new tests added: there is nothing to unit-test yet — the new code paths
  are logging-only diagnostics whose value is in a live recurrence, not in
  synthetic coverage.

## 2026-09-03 update: this was far more frequent than the original scope, and stopped exactly when diagnostics shipped

A user reviewing a workflow's Execution Logs panel found an old `failed`
entry with this exact error and asked what was wrong. Rather than treat it
as one more anecdote, `error LIKE '%receipt for child session%'` was run
against `background_agent_log` in every workflow's `db/db.sqlite` under
`workspace-docs/Workflow/`. Result: **56 occurrences across 8 workflows**
(`build-in-public` 13, `tectonicusadaytrading` 12, `social-media` 10,
`upwork` 10, `salesoutreach` 5, `linkedin` 4, `rtslatency` 1, `hetznerssh`
1), every one a `failed` background-task-agent row, spanning
**2026-08-19 through 2026-08-28** — i.e. this was a routine, near-daily
occurrence on several actively-scheduled workflows, not the rare edge case
the original P2/"blast radius of the symptom" framing assumed.

The more striking part: **zero occurrences after 2026-08-28**, the day the
diagnostic-only logging above shipped. Checked the three highest-frequency
workflows' most recent Pulse review rows (`build-in-public` through
2026-09-02, `social-media` through 2026-09-03, `upwork` through
2026-09-01) — all `completed`, none `failed`. Also checked this session's
own local `agent_go/logs/server_debug.log`: the three `[PULSE]
pulseRunIDForSession` lines present all resolved to a concrete, correct
session id (none logged "resolved to EMPTY"), and no
`requireBackgroundPulseReviewReceipts` failure or
`RecentPulseReviewRunIDsForModule` dump appears at all — meaning the
diagnostic path added for this ticket has not fired even once since it
shipped, on any workflow checked.

This does not confirm a fix — nothing in the diagnostic commit changed
control flow — but it is real evidence the failure mode either resolved
itself (a race narrowed or removed by an unrelated concurrent change in the
same window) or was tied to some other August-only condition (e.g. load,
timing, a since-changed scheduling cadence) rather than being evergreen.
Recommend downgrading urgency on active investigation unless a new
occurrence appears; the diagnostic logging should stay in place so the next
one (if any) is still self-diagnosing. Did not have access to production
scheduler logs (these workflows' Pulse runs are RTS-hosted, not local) to
look for an unrelated August 28-ish change that might explain the timing;
that would be the next thing to check if this recurs.

## Next step when this recurs

Pull the `[PULSE]` log lines around the failing background task agent's
session id from server logs at the time of the next occurrence, and compare:
what `pulseRunIDForSession` resolved `"current"` to during the reviewer's
`complete_pulse_review` call, versus what `config.MCPSessionID` was for the
parent background-task session at the time
`requireBackgroundPulseReviewReceipts` ran its check, versus the
`RecentPulseReviewRunIDsForModule` dump. That three-way comparison should
make the actual divergence (or confirm this was itself a stale/transient
finding, matching the PLAT-191/PLAT-195 pattern) obvious without further
guessing.
