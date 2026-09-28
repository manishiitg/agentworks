[← Pulse platform issue index](../../pulse_platform_issue_register.md)

## SQLite backlog reconciled — 2026-09-05

Resolved PUL-01B1E294, PUL-439F126E, PUL-C796CD97, PUL-F7362B0B in the corresponding workflow databases after checking
the existing implementation and passing focused regression tests. Full evidence
and the complete remaining inventory: [reconciliation audit](../../../audits/platform-backlog-reconciliation-2026-09-05.md).
This is internal tracking closure, not a claim of a new deployed end-to-end run.
Previous SQLite records are retained in audit events; unrelated findings remain
open. No business data or historical schedule outcome was rewritten.

## Diagnostic narrowed, and a new case on rtsaws — 2026-09-28

**Log line.** The `[PLAT-280]` log line checked only the session's
registered env for `WORKFLOW_DB_ACCESS` without `DB_PATH`. Every agentic step
matches that by design: only scripted steps get direct database access
(`isScriptedStep`), and agentic steps have `db.sqlite` on their blocked list
and use `query_workflow_db` / `mutate_workflow_db`. The line fired about 2,500
times a day on RTS as noise.

It now checks the command's final environment, and fires only when the session
was granted direct access (the database file is not blocked) yet `DB_PATH` is
missing (`shellMissingGrantedDBPath`). That is the real anomaly this ticket was
about.

**Same class on rtsaws.** `aws-infra-health` is typed `regular` (agentic), but
its job is a ported script (`main.py`) that writes `infra_daily_metrics`
straight into SQLite.
- It never gets `DB_PATH`, so the script skips saving.
- `infra_daily_metrics` has exactly one row, 2026-09-14, so the daily
  coverage number has been lost every day since.
- Goal Work noticed it ("infra map measurement was stale since
  mid-September"; "skipped saving the daily coverage row because DB_PATH was
  unset") but could not fix it, since promoting an existing step to scripted
  is the user's call.

Fix options, for the user:
- (a) convert the step to scripted, as was done for upwork;
- (b) make `main.py` write through `mutate_workflow_db`.

# PLAT-280 — `upwork`'s scripted-mode DB steps lose `$DB_PATH` because their plan type never matched their declared execution mode

| Coordination | Value |
|---|---|
| Assigned agent | Claude Code |
| Ticket state | `fixed` — root cause confirmed (not the session-id hypothesis below), a real conversion tool + write-time guard + workflow contract migration shipped, and upwork's 4 affected steps converted live |
| Last synchronized | `2026-09-04` |

- **Priority:** P1 — user-reported live. `search-save-jobs` is upwork's core
  "save shortlisted jobs to the database" step; when it fails, jobs found in
  a run are silently never saved or submitted, and upwork's own Pulse
  Technical Review has queued an operator decision ("How should job-search
  runs proceed while the results database cannot be opened?") because it
  cannot resolve this on its own.
- **Owner:** `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_message_sequence.go`
  (`setMessageSequenceShellEnv`), `controller_agent_factory.go`
  (`createExecutionOnlyAgent`, `registerStepSessionShellEnv`,
  `injectStepEnvIntoShellExecutor`), `agent_go/pkg/workspace/execute_shell_command.go`
  (`ExecuteShellCommand`, `sessionIDFromContext`), `agent_go/pkg/common/types.go`
  (`SetSessionShellEnv`/`GetSessionShellEnv`).
- **Related:** [PLAT-196](../pulse-governance/plat-196.md)) — a different symptom (Pulse review
  receipts) traced to the same unresolved category of question: how a
  session id resolves for a call that did not arrive as a plain in-process
  native tool call. Worth checking together if either recurs with fresh
  evidence.

## What was found

`workspace-docs/Workflow/upwork/db/db.sqlite`'s `background_agent_log` shows
the `search-save-jobs` message_sequence step's `verify-scripted-result` item
failing on every scheduled run since 2026-09-01, worded slightly differently
each time (agent-authored, not a fixed string) but the same failure every
time — most recently **today**, 2026-09-04T03:58:00Z:

- 2026-09-01T03:31–03:54Z, 15:31–15:56Z
- 2026-09-02T03:31–03:57Z
- 2026-09-04T03:31–03:58Z

Runs before 2026-09-01 (back through 2026-08-25) all `completed`. Three
other steps in the same workflow show the identical `run_concerns` diagnosis
(`bid-record`, `outreach-record`, `improve-read-history` — all of upwork's
DB-writing steps), each with `status='external_action_required'` since
2026-09-01T04:07Z, `seen_count=3`. Upwork's own `plan_drift_review` on
2026-09-01 wrote the clearest available diagnosis directly into
`run_concerns`:

> "search-save-jobs remains message_sequence while step_config declares
> scripted; the agentic runtime withheld a usable DB_PATH, so main.py failed
> with sqlite3.OperationalError and the required summary was never
> produced."

`step_config.json` confirms the shape: `search-save-jobs` has
`declared_execution_mode: "scripted"` and `use_code_execution_mode: true`,
but `plan.json` still types the step `message_sequence` — its own
`review_notes` (2026-08-31) explain why: *"The stable message-sequence
wrapper remains because the typed plan API has no in-place cross-type
conversion; it now delegates to the script."* This is a known, accepted
hybrid: a message_sequence step whose job is entirely "run the checked-in
`learnings/search-save-jobs/main.py`", which requires `$DB_PATH` per this
codebase's own standing instruction to scripted steps (`controller_scripted.go`
lines 776-780: *"insists steps use $DB_PATH and report an open failure as a
runtime bug"* — which is exactly what happened here; the agent behaved
correctly by refusing to work around it).

## What was traced and could not be faulted

The in-process injection path for exactly this case does the right thing on
paper:

1. `createExecutionOnlyAgent` computes `directDBAccess :=
   isScriptedExecutionModeConfig(stepConfig)` — reads
   `declared_execution_mode`, not the plan's step type, so it correctly
   returns `true` for `search-save-jobs` despite its `message_sequence` plan
   type.
2. When `true`, it resolves `dbAbsPath` and calls both
   `registerStepSessionShellEnv` (writes `DB_PATH` into the session's shared
   shell-env store, keyed by `config.MCPSessionID`) and
   `injectStepEnvIntoShellExecutor` (wraps the in-process
   `execute_shell_command` executor to inject `DB_PATH` into `extra_env` at
   call time).
3. `controller_message_sequence.go` separately calls
   `setMessageSequenceShellEnv` before and after agent creation, which always
   registers the session's env with an empty `dbAbsPath` (hardcoded `""`) and
   `directDBAccess=false` — **initially suspected as the clobbering bug**,
   but `common.SetSessionShellEnv` merges key-by-key into the existing map
   rather than replacing it (confirmed by reading its body), and an absent
   key in the merged map does not delete an existing one. This call does not
   erase a `DB_PATH` set moments earlier for the same session id. Ruled out.
4. `execute_shell_command.go`'s `ExecuteShellCommand` reads
   `sessionEnv := common.GetSessionShellEnv(sessionID)` and merges it into
   the request env (`mergeShellCommandEnv`), so a bridge-originated (HTTP)
   shell call for the same session id should see the registered `DB_PATH`
   too.

None of steps 1-4 show a confirmed defect from static reading. The remaining
open variable is `sessionID := c.sessionIDFromContext(ctx)` inside
`ExecuteShellCommand` (`pkg/workspace/client.go`): it prefers a
`common.ChatSessionIDKey` context value, falling back to the *client's own*
`MCP_SESSION_ID` extra-env entry — and this file's own comment two lines
above warns "Parallel Pulse reviewers share a Client, so aliasing the client
env here would let concurrent requests write ... into the same map." All
four affected steps use `codex-cli` as their provider with
`use_code_execution_mode: true` — an external CLI subprocess whose shell
calls arrive over the HTTP bridge rather than as native in-process tool
calls, the same "how does a non-native-in-process call's session id resolve"
question [PLAT-196](../pulse-governance/plat-196.md)) left open for a different symptom. Whether
a concurrent request on a shared `Client`, or a bridge-call session id that
does not match `config.MCPSessionID`, is the actual mechanism was not
possible to confirm without a live capture.

## What shipped: one diagnostic log line, no behavior change

`agent_go/pkg/workspace/execute_shell_command.go`, right after `sessionEnv`
is resolved in `ExecuteShellCommand`: if the session's registered env shows
`WORKFLOW_DB_ACCESS` set (i.e. this session was granted DB access) but
`DB_PATH` is empty, log `[PLAT-280]` with the resolved `sessionID`, the
`WORKFLOW_DB_ACCESS` value, and the client's own `MCP_SESSION_ID` — enough to
tell, on the next recurrence, whether the session id `ExecuteShellCommand`
resolved even matches the one `search-save-jobs` was assigned, and whether
this is a genuinely empty registration or a session-id mismatch. No control
flow changed.

## Explicitly not done

- No fix attempted to `sessionIDFromContext`, the shared-`Client` env
  aliasing risk, or the message-sequence/scripted hybrid pattern itself —
  none of these are confirmed broken, and guessing at a fix for an
  unconfirmed mechanism risks masking the real one.
- Did not attempt to give the plan a true in-place type conversion from
  `message_sequence` to `scripted` — upwork's own 2026-08-31 review already
  concluded the typed plan API has no safe way to do this; that is a
  separate, larger tooling gap than this ticket's failure.

## Verification

- `GOWORK=off go build ./pkg/workspace/...` clean.
- No tests added: the change is a logging-only diagnostic with no new
  branch to unit-test, matching this repo's convention for this class of
  fix (see PLAT-196).

## Next step when this recurs (superseded — see resolution below)

The paragraph below was the plan before the actual root cause was found; kept
for the record.

`search-save-jobs` runs on a schedule that fires roughly every 1-2 days
(most recently today); the next failure should carry the `[PLAT-280]` log
line. Compare the `sessionID` it names against the session id
`search-save-jobs` was actually assigned for that run (visible in the same
run's earlier `[BG AGENT]`/session-setup log lines) — a mismatch confirms
the session-id-resolution hypothesis and points directly at
`sessionIDFromContext`'s fallback or the shared-`Client` env; a match with
`DB_PATH` genuinely absent points elsewhere (worth then checking whether
`setMessageSequenceShellEnv`'s *first* call, before agent creation, ever
runs *after* the one inside `createExecutionOnlyAgent` in some ordering this
trace did not consider).

## Resolution: the session-id hypothesis was unnecessary — the actual defect was simpler

The user pushed back on the framing itself: *"does declared execution mode
even make sense... or should just step type be scripted, because regular
step is scripted right"*. That question found the real defect faster than
the diagnostic-logging plan above would have.

`RegularPlanStep` (plan `type: "regular"`) is already what the authoring
surface calls "scripted" — the tool that creates one is literally named
`add_scripted_step`. `declared_execution_mode` on a `regular` step exists
only to disambiguate it from a pre-message_sequence-era legacy `regular`
step that is really conversational (auto-upgraded to `message_sequence` by
`update_message_sequence_step` the moment it's edited — that compatibility
path already existed). It was never a valid, independent signal on a
`message_sequence`-typed step: a `message_sequence` step is dispatched
entirely through `controller_message_sequence.go`'s conversational executor
regardless of what its `declared_execution_mode` claims. The real scripted
executor (`controller_execution.go`'s `isScriptedMode` branch, backed by
`controller_scripted.go`) — the one that reliably sets `$DB_PATH` — is only
ever reached for a step whose **plan type** is `regular`. Setting
`declared_execution_mode: "scripted"` on a `message_sequence` step, as a
2026-08-31 technical-maintenance pass did for these four upwork steps
(because no tool existed to convert the plan type), created a step that
*claimed* to be scripted but never actually ran through the engine that
makes that claim true. No session-id mismatch, no race, no exotic bridge
behavior — the step simply never used the code path this whole
investigation was tracing.

**What shipped:**

1. **A real in-place conversion**, symmetric with the existing (and
   previously one-directional) `message_sequence` ← `regular` legacy-upgrade
   path. `update_scripted_step` now accepts a `message_sequence` step whose
   `step_config` already declares `scripted` and atomically converts it to
   `regular` while applying the edit — same id, `step_config.json`/drift
   history preserved, items dropped (a scripted step's real work is the
   checked-in `main.py`, not plan-authored items). New:
   `normalizeMessageSequenceStepToRegular`
   (`controller_message_sequence.go`), `prepareScriptedStepUpdateTarget`
   (replaces the old validate-only `validateScriptedStepUpdateTarget`,
   `planning_agent.go`).
2. **A write-time guard.** `update_step_config` already refused
   `declared_execution_mode="scripted"` on an `orchestrator` (`todo_task`)
   step; it now refuses the same combination on a `message_sequence` step
   too, pointing the caller at `update_scripted_step` instead of letting the
   drift get created again (`interactive_workshop_manager.go`).
3. **Workflow contract 1.0.37.** `workflowContractScriptedTypeStaysRegularVersion`
   walks every workflow through this same conversion the next time its
   scheduler-driven contract preflight runs, for any other workflow carrying
   the same drift (`workflow_manifest.go`, `workflow_version_upgrades.go`).
4. **upwork's four affected steps fixed live.** Ran the real
   `update_scripted_step` executor (not a hand-edit) against
   `workspace-docs/Workflow/upwork/planning/{plan,step_config}.json` for
   `search-save-jobs`, `bid-record`, `outreach-record`, and
   `improve-read-history`. All four are now `regular`-typed; verified by
   re-reading the plan afterward. `step_config.json`'s
   `description_reviewed` was cleared and `drift_review.needs_review` set —
   the tool's normal "this step's contract may be stale, re-review it"
   side effect, not a bug. upwork's `workflow.json` version was deliberately
   left at `1.0.35` (not hand-stamped to `1.0.37`) so its next scheduled
   contract preflight still runs the intervening `1.0.36` migration in
   order; the `1.0.37` step it eventually reaches will find nothing left to
   do.

**The `[PLAT-280]` diagnostic log line was left in place** — harmless, and
still useful as a tripwire if some other code path ever produces the same
symptom for a different reason.

**Not yet done:** confirming with a live scheduled run that
`search-save-jobs` actually saves jobs now (its cron fires roughly daily;
next occurrence should show `status='completed'` in
`background_agent_log`).
