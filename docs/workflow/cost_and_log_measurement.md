# Cost And Log Measurement

This doc explains how workflow costs and logs are measured now.

It is not the same as [workflow_monitoring.md](workflow_monitoring.md).

- `workflow_monitoring.md` is about the user-facing observability surfaces
- this doc is about the storage and measurement architecture underneath those surfaces

## Two Different Systems

There are two separate data planes:

### 1. Cost measurement
- driven by `token_usage` events from LLM calls
- persisted into the workflow `costs/` ledger
- aggregated by run folder, phase, step, and model

### 2. Workflow logs
- written explicitly by workflow controllers as files under `runs/.../logs/`
- store execution results, conversations, validation results, learning traces, orchestration traces, and markers

Costs are not reconstructed from log files.
Logs are not reconstructed from cost files.

## Cost Measurement Source Of Truth

The source of truth for workflow cost measurement is the `token_usage` event stream.

Current flow:

1. An agent emits a `token_usage` event.
2. The context-aware bridge intercepts that event.
3. It extracts:
   - provider
   - model ID
   - prompt/input tokens
   - completion/output tokens
   - cache read and cache write tokens
   - reasoning tokens
   - LLM call count
4. It persists those values directly to the `costs/` ledger.

This happens in [context_aware_bridge.go](../../agent_go/pkg/orchestrator/context_aware_bridge.go).

## What Gets Counted

The cost ledger tracks per-model usage with pricing fields:

- input tokens
- output tokens
- cache tokens
- cache read tokens
- cache write tokens
- reasoning tokens
- LLM call count
- input cost
- output cost
- reasoning cost
- cache read cost
- cache write cost
- total cost
- context window usage

The main types are defined in:
- [base_orchestrator_types.go](../../agent_go/pkg/orchestrator/base_orchestrator_types.go)
- [cost_storage.go](../../agent_go/pkg/orchestrator/cost_storage.go)

Important nuance:
- cache reads and cache writes are tracked separately
- pricing is normalized or recomputed from model pricing helpers when files are read or merged
- the ledger stores both per-model totals and per-step-per-model totals

## Cost Storage Layout

The current storage layout is under `costs/`.

### Phase-only costs

Used for phase agents such as planning and builder-style workflow-phase sessions.

Files:
- `costs/phase/token_usage.json`
- `costs/phase/daily/YYYY-MM-DD.json`

These are written by `PersistPhaseTokenUsage(...)` in [base_orchestrator_tokens.go](../../agent_go/pkg/orchestrator/base_orchestrator_tokens.go).

Builder workflow-phase sessions also write phase cost data directly in [server.go](../../agent_go/cmd/server/server.go).

### Execution run costs

Used for normal workflow execution runs.

Files:
- `costs/execution/<group-or-__ungrouped__>/YYYY-MM-DD.json`

Each daily file stores a map of run folders:
- `run_folders[runFolder] = TokenUsageFile`

So one daily bucket can contain multiple runs for the same group on the same UTC date.

### Evaluation run costs

Used for evaluation execution.

Files:
- (retired) `costs/evaluation/<group-or-__ungrouped__>/YYYY-MM-DD.json` — legacy ledger, no new writes

Evaluation uses the same ledger shape as execution, but under the `evaluation` scope.

## Aggregation Shape

Run-level cost data is aggregated into two main maps:

- `by_model`
- `by_step_and_model`

`by_model`:
- total usage across the whole run for each model

`by_step_and_model`:
- nested usage keyed by step and model
- current key format is usually `phase:stepID`
- older fallback format may use `phase:stepIndex`

This is what lets the UI render:
- stage totals
- step-wise totals
- model-wise totals
- per-run totals

## Legacy Compatibility

Older layouts still exist in some workspaces:

- `runs/<runFolder>/token_usage.json`
- (retired) `evaluation/runs/<runFolder>/token_usage.json` — legacy path, no new writes
- workspace-root `token_usage.json` for phase costs

Current behavior:
- these are migrated into the `costs/` ledger when read or written
- migration is handled automatically by the cost storage helpers

This logic is in [cost_storage.go](../../agent_go/cmd/server/cost_storage.go) and [token_usage_store.go](../../agent_go/pkg/orchestrator/token_usage_store.go).

## How `/api/workflow/costs` Works

The workflow cost API does not read a single per-run JSON file anymore.

Current behavior:
- reads `costs/phase/token_usage.json`
- reads `costs/phase/daily/*`
- reads all execution daily ledgers under `costs/execution/*`
- (retired) used to read evaluation daily ledgers under `costs/evaluation/*`
- merges run-folder totals across daily files
- returns execution and evaluation cost data side by side for each run folder

This happens in:
- [workflow.go](../../agent_go/cmd/server/workflow.go)
- [cost_storage.go](../../agent_go/cmd/server/cost_storage.go)

## Step Token Usage Events

`step_token_usage` is a derived summary event, not the primary source of truth.

Current behavior:
- the system reads the persisted cost ledger for the current run
- aggregates the requested step across all models
- emits a summary event for UI display

This means:
- `token_usage` events drive persistence
- `step_token_usage` events summarize persisted step totals

See [base_orchestrator_tokens.go](../../agent_go/pkg/orchestrator/base_orchestrator_tokens.go).

## Workflow Log Source Of Truth

Workflow logs are explicit artifact files written by workflow controllers.

They are not reconstructed from the database event stream.

The main log families are:

- execution result JSON
- execution conversation JSON
- execution prompts JSON
- validation JSON
- pre-validation JSON
- learning execution JSONL
- learning conversation JSON
- orchestration execution JSONL
- todo-task execution JSONL

## Log File Layout

For a normal run folder, logs live under:

- `runs/{runFolder}/logs/...`

Common files:

- `logs/{step-folder}/execution/execution-attempt-{N}-iteration-{M}.json`
- `logs/{step-folder}/execution/execution-attempt-{N}-iteration-{M}-conversation.json`
- `logs/{step-folder}/execution/execution-attempt-{N}-iteration-{M}-prompts.json`
- `logs/{step-folder}/validation.json`
- `logs/{step-folder}/validation-{N}.json`
- `logs/{step-folder}/pre_validation.json`
- `logs/{step-folder}/learning-execution.json`
- `logs/{step-folder}/learning-conversation.json`
- `logs/{step-folder}/orchestration-execution.json`
- `logs/{step-folder}/todo-task-execution.json`

There are also builder/session logs under:

- `builder/conversation/YYYY-MM-DD/session-{sessionId}-conversation.json`

## Who Writes The Logs

Main writers:

- execution result and conversation logs:
  [controller_execution.go](../../agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_execution.go)
- learning logs:
  [controller_learning.go](../../agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_learning.go)
- pre-validation logs:
  [pre_validation.go](../../agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/pre_validation.go)
- workflow log API scanner:
  [workflow.go](../../agent_go/cmd/server/workflow.go)

## Archived Logs

When a step is re-executed, older log files can be archived instead of being discarded.

Current behavior:
- selected log files are moved into `archived/{timestamp}` under the step log folder
- this preserves older attempts for debugging while keeping the active log folder readable

This is handled in [controller_progress.go](../../agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_progress.go).

## How `/api/workflow/logs` Works

The workflow log API scans the run folder and reconstructs a structured view for the UI.

Current behavior:
- scans `runs/{runFolder}/logs/`
- scans `runs/{runFolder}/execution/`
- maps step folders back to plan metadata from `planning/plan.json`
- parses known file families into typed response sections
- exposes conversation file paths for lazy loading

This is why the execution log popup can show:
- per-step execution attempts
- validation history
- learning traces
- orchestration and todo-task traces

The implementation is in [workflow.go](../../agent_go/cmd/server/workflow.go).

## Practical Summary

Use this mental model:

- costs come from `token_usage` events and live in `costs/`
- logs come from controller-written files and live in `runs/.../logs/`
- `step_token_usage` is a derived UI summary from persisted costs
- `/api/workflow/costs` merges the cost ledger
- `/api/workflow/logs` scans and structures the run artifacts

## Related Docs

- [workflow_monitoring.md](workflow_monitoring.md)
- [iteration_run_folder_architecture.md](iteration_run_folder_architecture.md)
- [evaluation_system.md](./evaluation_system.md)
