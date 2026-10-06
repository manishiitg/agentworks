[← platform / frontend-chat](index.md)

# PLAT-254 — Execution Logs panel showed misleading recency data; `execute_step` never stated which run_folder/group it actually used

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | platform |
| Area | frontend-chat |
| Summary | P1 — reported live by the user while actively debugging a real |

| Coordination | Value |
|---|---|
| Assigned agent | Claude Code |
| Ticket state | `implemented` |
| Last synchronized | `2026-08-30` |

- **Priority:** P1 — reported live by the user while actively debugging a real
  workflow (`confida-login`), with screenshots at each step.
- **Owner:** `frontend/src/components/workflow/ExecutionLogsPopup.tsx`,
  `frontend/src/components/workflow/canvas/WorkflowCanvas.tsx`,
  `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/interactive_workshop_manager.go`.

## What happened

The user pinned a step's LLM to `gemini-3.7-flash` via `update_step_config`,
re-ran it, and the Execution Logs panel's step-summary row kept showing the
old model (`claude-haiku-4-5`) instead. Investigation (with live disk reads
against the real workflow, not guesses) found four separate, real defects
under one umbrella — a step's "Attempt N" number is a **fixed retry-slot
label, not chronological order**: a fresh top-level re-run overwrites the
"attempt 1" slot's file while an unrelated, much older "attempt 2" from a
completely different run can sit right next to it, hours or days apart.
Confirmed live: `execution-attempt-1-iteration-0.json` had `completed_at`
`2026-08-30T04:44:02Z` (the new gemini-3.7-flash run) while
`execution-attempt-2-iteration-0.json` still held `2026-08-29T17:43:22Z`
(the old claude-haiku run) — attempt 2 was ~11 hours *older* than attempt 1.

1. **`getStepModel()` picked the wrong attempt.** It walked the executions
   array backwards and returned the first non-fast-path model found,
   assuming the last array element was newest. It wasn't — it returned
   attempt 2's (older) model instead of attempt 1's (actually newest).
2. **The "Validations" section labeled every entry "Validation attempt 1"**
   regardless of which of several genuinely different validation phases/
   executions it was. Real workflow data showed 5 validation records
   spanning `initial-check`, `saved-script`, and two separate `final-gate`
   executions (`execution_001`, `execution_002`) — all rendered with the
   same useless "1", because the field used (`validation_attempt`) is
   nearly always 1 and doesn't distinguish phase or execution slot at all.
3. **No datetime shown anywhere in the attempt/validation lists**, so
   recency was unverifiable from the UI at all — the only way to tell which
   attempt was actually newest was to read the raw JSON files' `started_at`/
   `completed_at`/`timestamp` fields directly on disk.
4. **`execute_step`'s response never stated which `run_folder`/group it
   resolved to.** `group_name` is optional on the tool; when omitted, the
   handler silently falls back to `enabledGroupNames[0]` or the first
   variables-manifest group. If that differs from the group the user
   actually meant, the run writes real logs into a sibling folder
   (`runs/iteration-0/<other-group>/`) with zero indication anything went
   to the "wrong" place.

Separately, but discovered investigating the same "stale panel" report: the
panel's own refresh icon (`RefreshCw` in the header) only ever re-fetches
logs for the *currently selected* run folder — it never refreshes the
run-folder list itself (`runFolders`/the dropdown). That list comes from
`workspaceState.run_folders` in the parent canvas, cached by
`useWorkspaceState()` and only invalidated by the *outer* canvas "Refresh"
button — a different control entirely. A run folder that appeared after the
panel last loaded stayed invisible in the dropdown no matter how many times
the panel's own refresh was clicked.

Two live-checked reports that turned out **not** to be bugs, worth recording
so they aren't re-litigated: (a) a step's `description` length shown in the
panel ("9.4k instr") exactly matched the current on-disk `plan.json` value
(9362 chars) — the user had believed they'd edited that step's description
down, but the changelog showed no such edit; they'd actually edited a
*different* step. (b) That different step's description length (7096 chars,
"7.1k instr") also exactly matched two real, changelog-recorded edits in
sequence (9173→10256→7096) — the panel was accurate both times, not stale.

## Fix

- `getStepModel()` (`ExecutionLogsPopup.tsx`) now compares each execution's
  real `completed_at`/`started_at` timestamp and returns the model from the
  one with the latest timestamp, not the last array element.
- "Attempt N" rows (`ExecutionLogsPopup.tsx`) now show a real datetime chip
  (`completed_at` falling back to `started_at`), so recency is never a guess.
- Validation rows now label with the real distinguishing fields —
  `validation_phase` (`initial-check`/`saved-script`/`final-gate`) and
  `execution_attempt` — plus a real datetime, instead of a `validation_attempt`
  number that's nearly always 1.
- `execute_step`'s immediate response (`interactive_workshop_manager.go`) now
  includes `run_folder: "<resolved>"` explicitly, with an inline note when no
  group could be resolved at all (no defined variable groups in the
  workspace).
- The panel's refresh button now also calls a new optional
  `onRefreshRunFolders` prop (wired to `WorkflowCanvas.tsx`'s existing
  `refreshWorkspaceState()` — the same function the outer canvas "Refresh"
  button already used), so a newly-appeared run folder is discoverable from
  the panel's own refresh, not only the outer canvas one.

Also folded into the same pass (found while reading this component's
`ConversationViewer.tsx` sibling): the "FULL CALL TIMELINE" summary inside
each execution's conversation view previously showed only timing metadata
per call (name/duration/tokens) with no way to see the actual tool-call
arguments or LLM response — every row is now expandable in place, reusing
the same `ConversationToolCallDisplay`/`ConversationToolResponseDisplay`
components the full conversation history already uses, matched back to the
real content by `tool_call_id`. The "Back to all steps" sticky header (a
separate, earlier live report) was also made to shrink on scroll instead of
permanently eating vertical space, with hysteresis (different scroll
thresholds to shrink vs. re-expand) after the first version was reported to
flicker on tiny scroll movements.

## Explicitly not done

- `onRefreshRunFolders` was wired only into `WorkflowCanvas.tsx`'s embedded
  panel usage — the three other call sites
  (`WorkflowsOverviewPage.tsx`, `EmployeeDashboard.tsx`,
  `WorkflowScheduleRunsPanel.tsx`) have their own, different run-folder data
  flows and were left unwired; the prop is optional so nothing regresses
  there, they just don't get the same refresh-scope fix yet.
- Did not add stricter validation rejecting an ambiguous/omitted
  `group_name` when a workspace has multiple groups — only surfaced what was
  actually resolved. A hard requirement was considered but not requested;
  this ticket scoped to "never silently ambiguous," not "never optional."

## Verification

- `go build ./...` clean; `pkg/orchestrator/agents/workflow/step_based_workflow`
  full test suite passes (0 failures) after the `execute_step` response change.
- `npx tsc --noEmit -p .` and `npm run build` clean after every frontend
  change in this ticket.
- Root cause for the model-recency bug confirmed against real, live
  `confida-login` workflow data (not synthetic) — actual `completed_at`
  timestamps read directly from the two attempt files before writing the fix.

## Reverify

Re-run a step whose LLM config was just changed, confirm the step-summary
row's model chip shows the new model immediately (no manual attempt-order
reasoning required), and confirm a standalone `execute_step` run's folder
shows up in the panel's dropdown after clicking the panel's own refresh
button, without needing the outer canvas refresh.

## Follow-up — Pulse background review logs

Execution Logs now includes a separate **Pulse reviews** section for the
selected retained run. Each manual or scheduled Pulse pass remains a distinct,
timestamped entry and exposes its background review agents, status, duration,
provider/model, parent orchestrator execution ID, original instruction,
assistant messages, tool calls, result, and errors from the durable structured
transcript. Ordinary workflow step sub-agents are excluded: scheduled sessions
use the full-run completion boundary, while manual Pulse sessions use their
explicit `manual-pulse` schedule identity. Transcript read/parse failures are
shown instead of being mistaken for an empty review.

Verification added in `workflow_pulse_execution_logs_test.go` covers scheduled
post-run selection, group-aware retained-folder matching, transcript loading,
and manual Pulse runs without a full-run lifecycle marker. `go test
./cmd/server` and frontend TypeScript compilation pass.
