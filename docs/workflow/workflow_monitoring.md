# Workflow Monitoring

This doc covers the workflow observability surfaces that still exist in the product today.

It is worth keeping. The workflow UI still exposes execution logs, cost analysis, evaluation reports, learnings, and run history. What changed is the architecture behind them: older validation-heavy and per-step-learning explanations are no longer the right model.

## Monitoring Surfaces

There are three practical scopes:

### 1. Workflow-level views
- **Goals & metrics**: purpose, success criteria and saved source-backed readings
- **Costs**: aggregated token and USD usage across run folders
- **Evaluation reports**: benchmark-style scoring across runs, with single-run drill-down
- **Learnings**: current persisted learning state, including the global workflow skill

### 2. Run-folder views
- **Execution logs**: detailed logs for one selected run folder such as `iteration-12` or `iteration-12/group-a`
- **Final outputs**: generated final reports for a run folder

### 3. Cross-workflow operational views
- **Workflow overview**: recent run folders, status, timestamps, costs, and measurement presence across workflows
- **Scheduled runs panel**: cron job history, latest runs, live sessions, and drill-down into logs/costs/measurement for scheduled executions

## Goals and metrics

Goals & metrics shows the Goal's purpose, success criteria and saved measurement
history. The dashboard, run logs and Costs provide execution evidence. Decisions
hold questions for the owner. AgentWorks can review a linked Goal using its
existing access, shared skills and ordinary schedules; no built-in engine runs
after a workflow.

## Execution Logs

Execution logs are still the main debugging surface for a single run folder.

Current behavior:
- The popup is opened from the workflow toolbar and is scoped to the currently selected run folder when possible.
- Backend data comes from `/api/workflow/logs`.
- The server scans `runs/{runFolder}/logs/` and `runs/{runFolder}/execution/`, then maps folder names back to plan step metadata from `planning/plan.json`.

The log viewer still supports these file families:
- `execution/execution-attempt-{A}-iteration-{I}.json`
- `execution/execution-attempt-{A}-iteration-{I}-conversation.json`
- `validation.json` and `validation-{N}.json`
- `learning-execution.json`
- `orchestration-execution.json`
- `todo-task-execution.json`

Important current nuance:
- validation logs still exist in the execution log viewer, but validation is no longer the main architecture story for workflow docs
- pre-validation remains relevant runtime signal, but the canonical validation doc is [pre_validation_guide.md](pre_validation_guide.md)
- execution logs are best thought of as per-run forensic data, not as the source of workflow architecture truth

## Costs

Cost analysis is still current and useful.

Current behavior:
- The popup is opened from the workflow toolbar, overview page, and scheduled runs panel.
- Backend data comes from `/api/workflow/costs`.
- It aggregates token usage across run folders and supports drill-down into individual runs.
- The UI highlights the currently selected run folder when applicable.

The cost UI is still workflow-level, not step-config architecture.

## Evaluation Reports

Retired with the eval subsystem. The `/api/workflow/evaluation-reports`
endpoint and its All Iterations / Single Iteration UI are gone; old
`evaluation_report.json` files on disk are read-only history. Outcome
visibility now comes from producer outputs, goal observations
(`get_goal_metrics`), and the dashboard.

## Learnings

The learnings popup is still relevant, but the old explanation was stale.

Current behavior:
- Backend data comes from `/api/workflow/learnings/all`.
- The popup shows both step metadata and the global workflow learning entry `_global`.
- `_global` is rendered as **Workflow Knowledge (Global)** in the UI.
- Locking and unlocking still exist via step config updates.
- Deleting learnings for a step still exists.

What changed:
- learning is no longer best described as per-step prose memory with complexity caps and exploitation phases
- the main persistent learning surface is now the shared skill at `learnings/_global/SKILL.md`
- scripted steps can still have step-specific saved code such as `learnings/{step-id}/main.py`

The canonical architecture doc for this is [learning_architecture.md](learning_architecture.md).

## Run Overview And Scheduled Runs

There are now two additional monitoring surfaces worth acknowledging:

### Workflow overview
- lists run folders from consolidated workspace state
- reconciles run metadata with active in-memory executions
- shows status, timestamps, cost summary, and evaluation presence across workflows

### Scheduled runs panel
- shows cron jobs and run history
- opens the same logs, costs, evaluation, and final-output popups for scheduled executions
- helps operational users inspect automation outcomes without opening each workflow canvas first

## Keep Or Remove?

Keep it.

This doc is still relevant because the product clearly has workflow monitoring and observability features. It just needed to stop documenting the older validation-era and complexity-cap model.

## Related Docs

- [cost_and_log_measurement.md](cost_and_log_measurement.md)
- [pre_validation_guide.md](pre_validation_guide.md)
- [learning_architecture.md](learning_architecture.md)
- [evaluation_system.md](./evaluation_system.md)
- [workflow_manifest_architecture.md](workflow_manifest_architecture.md)
