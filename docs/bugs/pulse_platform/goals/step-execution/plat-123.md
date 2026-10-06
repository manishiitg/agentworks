[← goals / step-execution](index.md)

## Internal reconciliation — 2026-09-05

PUL-D50AD8BC: superseded by subsequent deliberate ordinary-step record_run_concern retirement (PLAT-211). Current default/explicit step-tool tests confirm exclusion; do not restore the tool to satisfy the obsolete requirement.

Resolved in SQLite for internal tracking with previous concern/detail records
preserved in resolution events. Source/tests verified; deployed replay and
historical business/module-result repair are not claimed. Full mapping:
[remaining-report audit](../../../../audits/platform-open-report-reconciliation-2026-09-05.md).

# PLAT-123 — record_run_concern was never registered for the default workflow-step tool path

| Field | Value |
|---|---|
| State | closed |
| Priority | P2 |
| Product | goals |
| Area | step-execution |
| Summary | P2 — the tool silently no-ops for most workflow steps rather |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `implemented`, live-reverify pending on a fresh run |
| Last synchronized | `2026-08-17` |

- **Priority:** P2 — the tool silently no-ops for most workflow steps rather
  than breaking the run; a step's own work still completes, but its findings
  have nowhere structured to go, so PLAT-055's whole reason to exist (getting
  defects out of learnings and into a real record) quietly did not apply to
  most steps.
- **Owner:** `pkg/orchestrator/agents/workflow/step_based_workflow/
  controller_agent_factory.go` (`applyStepConfigToAgentConfig`,
  `createTodoTaskOrchestratorAgent`, `prepareCustomTools`)
- **Related:** [PLAT-055](../../platform/learnings-knowledge/plat-055.md)) (introduced `record_run_concern`),
  [PLAT-057](../pulse-governance/plat-057.md)), [PLAT-058](../../platform/learnings-knowledge/plat-058.md)) (both cite
  `record_run_concern` working — via the one call site that already force-
  included it, not representative of the default path)

## What was wrong

Confirmed live (2026-08-17), workflow `confida-login`, group `confida-staging`,
step `survey-app-and-refresh-knowledge` (a todo-task-orchestrator execution
agent): its reflection turn's own prompt names `record_run_concern` and
instructs the model to call it for any defect observed. The call failed:

```
tools_unavailable: unknown=[record_run_concern]: these names are not
registered by any currently connected server. Registered tools for this
session: [agent_browser answer_human_input_request create_human_input_request
diff_patch_workspace_file execute_shell_command generate_music
generate_text_llm generate_video get_human_input_request human_feedback
image_edit image_gen mark_human_input_consumed mutate_workflow_db notify_user
query_workflow_db read_image read_skill search_web_llm speech_to_text
text_to_speech]
```

`query_workflow_db`/`mutate_workflow_db` are present only because this
particular workflow's own declared `selected_tools` happen to include them —
nobody would think to manually declare a platform-internal concern-reporting
tool, so any workflow that did not happen to also select `workflow_db:*`
tools would never see `record_run_concern` either.

## Root cause

Three places build a step agent's tool list. All three set up the trusted
run-concern session identity correctly (`configureRunConcernSession`), so
`common.GetRunConcernSessionContext` would have resolved fine — but none of
them ever added `"workflow_db:record_run_concern"` to the tool list itself:

1. `applyStepConfigToAgentConfig` — the primary step-execution path (both the
   step-specific-`SelectedTools` branch and the orchestrator-default branch).
2. `createTodoTaskOrchestratorAgent` — the todo-task-orchestrator path (same
   two-branch shape). This is the one the confida-login evidence above traced
   through.
3. `prepareCustomTools`'s **default** branch (`defaultEnabledTools`, used when
   a step has no `EnabledCustomTools` allowlist).

The one call site that already got this right — `prepareCustomTools`'s
`EnabledCustomTools`-set branch — has its own comment explaining exactly the
principle the other three should have followed: *"Workflow DB tools are
capability-derived, not model-selected... every step can raise a structured
concern... a custom allowlist must not be able to silence that channel."* That
reasoning was implemented in one place and silently absent from the other
three.

The comment directly above `configureRunConcernSession`'s call site in
`createTodoTaskOrchestratorAgent` already claimed *"record_run_concern is
registered for every workflow-mode session, so the orchestrator is offered
the tool whether or not it can use it"* — that was the intent, never actually
implemented. Aspirational comments describing intended behavior next to code
that does not yet do it are exactly how this kind of gap survives review.

## Fix

Added the same unconditional force-include (`workflow_db:record_run_concern`,
deduped against an existing entry) to all three call sites, matching the
already-correct fourth. Verified via two new tests exercising the exact
default-branch gap:
`TestApplyStepConfigToAgentConfigAlwaysIncludesRunConcernTool` (both the
default and step-specific-`SelectedTools` sub-cases) and
`TestPrepareCustomToolsDefaultBranchIncludesRunConcernTool`. Deliberately did
**not** touch `prepareWorkspaceToolsOnly` (KB maintenance agents) — it has no
`configureRunConcernSession` call for its own session at all, so adding the
tool without the identity would just produce a different error
("no trusted step identity"); wiring up a KB-maintenance-agent's own concern
identity is a separate, bigger change with its own design questions (what
step ID would it attribute to?).

## Not yet confirmed

**No fresh live run has exercised the fix.** The confida-login run that
surfaced this bug was already in flight against the old binary when the fix
landed, and will not pick it up until its server process is rebuilt and
restarted. Verifying `record_run_concern` actually succeeds end-to-end on a
freshly-started default-path step (not just that it appears in
`SelectedTools` in a unit test) is the next step before closing this out.

## Acceptance

- A workflow step using the orchestrator's default tool set (no
  `EnabledCustomTools`, no step-specific `SelectedTools` naming it) can
  successfully call `record_run_concern` and have it recorded.
- The todo-task-orchestrator path shows the same on its own dedicated session.
- Confirmed via `[PRODUCT_TOOL_GATE]`-equivalent server log evidence on a
  live run, not just a passing unit test.
