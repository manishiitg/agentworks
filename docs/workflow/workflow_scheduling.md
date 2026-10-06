# Workflow Scheduling

Workflow scheduling is a first-class workflow feature.

The current system is fully file-backed:

- schedule definitions live in `workflow.json`
- global scheduler state lives in `config/scheduler.json`
- per-workflow schedule run history lives in `schedule-runs.json`

There is no DB-backed workflow scheduler architecture anymore.

Remote workspace note: in the planned Remote Workspace Gateway model, the
schedule files still live with the workflow on the server, but the server does
not run the coding agent. An online local runner reads the schedule, claims a
server-side job lease, executes the scheduled workshop messages locally, and
writes run history/Pulse/report artifacts back through the gateway. See
[Remote Workspace Gateway + Local Runner Plan](../core/remote_workspace_server_plan.md).

## Source Of Truth

Each workflow manifest can define zero or more schedules:

- `Workflow/<name>/workflow.json`

Current manifest schedule fields are defined in [workflow_manifest.go](../../agent_go/cmd/server/workflow_manifest.go):

- `id`
- `name`
- `description`
- `cron_expression`
- `timezone`
- `enabled`
- `trigger_payload`
- `group_ids`
- `mode` (`workshop` for workflow schedules; legacy `workflow` values are normalized)
- `messages`
- `workshop_mode`

Validation rules that matter now:

- every schedule must have an `id`
- every schedule must have a valid `cron_expression`
- every schedule must include at least one valid `group_id`
- `group_ids` are validated against `variables/variables.json`

That means schedules are always group-aware now. A schedule without valid group selection is rejected.

## Storage Layout

### Workflow-local schedule definitions

Schedule definitions are persisted in:

- `Workflow/<name>/workflow.json`

They belong to the workflow manifest alongside capabilities, ownership, and execution defaults.

### Global scheduler config

Global scheduler pause and execution flags are persisted in:

- `config/scheduler.json`

Current fields are defined in [scheduler_config_store.go](../../agent_go/cmd/server/scheduler_config_store.go):

- `globally_paused`
- `paused_at`
- `paused_by`
- `updated_at`
- `execution_enabled`
- `disabled_via_env`
- `disabled_reason`
- `allowed_workflows` (computed from env)
- `blocked_workflows` (computed from env)
- `allowed_users` (computed from env)
- `blocked_users` (computed from env)

Important distinction:

- `globally_paused` is persisted user-controlled state
- `execution_enabled` is computed runtime state

If `SCHEDULER_ENABLED=false`, automatic cron execution is disabled on that server, but manual trigger still works.

### Per-workflow run history

Schedule run history is persisted per workflow in:

- `Workflow/<name>/schedule-runs.json`

Entries are defined in [schedule_runs.go](../../agent_go/cmd/server/schedule_runs.go):

- `id`
- `schedule_id`
- `run_folder`
- `session_id`
- `status`
- `error`
- `duration_ms`
- `group_ids`
- `started_at`
- `completed_at`

The file keeps the newest entries first. Workflow history retains terminal
runs for at least 90 days; older terminal entries are pruned when a new run
is recorded. Active and undated runs are retained. The UI and
`get_schedule_runs` page this history with `limit` and `offset`. Product
schedule stores outside `Workflow/` retain their separate 200-entry cap.

## Runtime Model

The scheduler service is implemented in [scheduler.go](../../agent_go/cmd/server/scheduler.go).

On startup it:

- scans workflow workspaces for `workflow.json`
- loads enabled manifest schedules into `gocron`
- indexes `schedule_id -> workspace`
- computes next-run timestamps
- marks stale `running` entries in `schedule-runs.json` as `error` after restart

Runtime-only state is kept in memory per schedule:

- last status
- last run time
- next run time
- last session id
- last error
- last duration
- run count
- consecutive failures

That runtime state is not written back into `workflow.json`.

## Execution Mode

Workflow schedules use the workflow-phase transport (`mode = workshop`, `agent_mode = workflow_phase`). Normal scheduled messages execute with `workshop_mode = run`, which gives them the constrained Run prompt, tool catalog, projected skills, and—when CLI isolation is enabled—a private runtime working directory. The old direct orchestrator schedule mode (`mode = workflow`, `agent_mode = workflow`) is no longer generated or executed. Existing manifests with `mode = workflow` are normalized to the workflow-phase transport at runtime.

Pending contract upgrades are not schedule preflight turns. Cron/calendar runs,
including runs started through `trigger_schedule`, continue against the saved
workflow contract and never authorize, apply, or stamp a migration. Owners start
upgrades manually from the interactive Builder chat; interactive
`run_full_workflow` and `execute_step` calls remain blocked until the required
migrations are complete. Direct API/webhook triggers also remain fail-closed on
an incompatible contract.

Answered-decision preflight turns temporarily use `workshop_mode = workshop`
because they are explicitly allowed to update workflow artifacts. Post-run
Pulse turns also use Workshop mode. The scheduler switches modes per turn, so a
normal unattended run never inherits the maintenance surface.

Multi-agent schedules remain separate under `_users/{userID}/multiagent-schedules.json`.

### `workshop` mode

The scheduler builds a request with:

- `agent_mode = workflow_phase`
- `phase_id = workflow-builder`
- `triggered_by = cron`
- `execution_options.run_mode = use_same_run`
- `execution_options.selected_run_folder = iteration-0`
- `execution_options.execution_strategy = start_from_beginning_no_human`
- `execution_options.workshop_mode = run` for normal schedule messages
- `execution_options.enabled_group_ids = schedule.group_ids`

Then it sends the configured `messages[]` one by one and waits for the workshop session to become idle after each message.

If no messages are provided, it defaults to:

- `Run the full workflow using run_full_workflow tool.`

Run-mode workshop schedules are not limited to full workflow execution. The configured message can ask Run mode to answer directly from KB/learnings/db/run state, execute a targeted normal step, execute an orphan utility step, or call `run_full_workflow`, depending on the scheduled job.

## Groups And Run Folders

Schedules are always tied to variable groups.

Current implications:

- group IDs are required at save time
- scheduled executions pass those group IDs into workflow execution options
- workflow schedules start from `iteration-0`

There is helper logic for resolving a group-scoped workshop run folder, but the standard workshop scheduler request still starts from `iteration-0`.

That means scheduled runs follow the same broader run-folder model documented in [iteration_run_folder_architecture.md](iteration_run_folder_architecture.md).

## Auto Report Generation

Workshop schedules have one extra behavior.

If:

- `mode = workshop` or legacy `mode = workflow` normalized to workshop
- `workshop_mode` is `run`, legacy `runner`, or omitted
- none of the scheduled messages explicitly invoke `run_full_report`

then the scheduler tries to auto-generate the final report after the workshop message sequence completes.

That flow lives in [scheduler.go](../../agent_go/cmd/server/scheduler.go#L684).

One nuance in current code:

- final report generation requires a group-scoped run folder like `iteration-0/<group>`
- the workshop scheduler path itself still initializes from plain `iteration-0`

So report auto-generation for workshop schedules is coupled to the resolved run-folder shape, not just to the presence of a schedule.

## APIs

Scheduler APIs are registered in [scheduler_routes.go](../../agent_go/cmd/server/scheduler_routes.go):

- `GET /api/scheduler/config`
- `PUT /api/scheduler/config`
- `GET /api/scheduler/jobs`
- `POST /api/scheduler/jobs`
- `GET /api/scheduler/jobs/{id}`
- `PUT /api/scheduler/jobs/{id}`
- `DELETE /api/scheduler/jobs/{id}`
- `POST /api/scheduler/jobs/{id}/enable`
- `POST /api/scheduler/jobs/{id}/disable`
- `POST /api/scheduler/jobs/{id}/trigger`
- `POST /api/scheduler/jobs/{id}/stop`
- `GET /api/scheduler/jobs/{id}/runs`

The API response shape is a compatibility wrapper around:

- manifest schedule definition
- in-memory runtime state
- per-workflow run history

## Product Schedules

A product can declare recurring jobs of its own in `product.yaml`, under
`profile.schedules`. They are not workflow schedules: there is no manifest,
no run folder and no Pulse review. Each one runs the product's agent profile
for a user by sending its messages one at a time into that user's product
conversation, the same conversation the product surface shows.

```yaml
profile:
  runtime:
    conversation:
      mode: singleton          # required: schedules run in the one product chat
  schedules:
    - id: daily-checkin
      name: Daily check-in
      description: Review yesterday and send a summary
      enabled: true            # the product default; each user can override
      cron_expression: "0 8 * * *"
      timezone: Asia/Kolkata
      messages:
        - Review what changed since your last check-in and note anything worth flagging.
        - Send the summary with notify_user.
```

The definition and the timing rule live in `agent_go/pkg/productschedule`
(`Schedule`, `Validate`, `Decide`). Besides cron there is a cadence form
(`cadence_hours` with an optional `preferred_hour`) and a quiet rule
(`quiet_minutes`, `max_deferral_hours`) for products that run on their own
and know when the user was last active; the platform runs cron schedules and
ignores the quiet rule.

On the AgentWorks server `cmd/server/product_schedules.go` runs them:

- **Who**: every enabled directory user whose product access includes the
  product (admins and unrestricted members included), or the single local
  user when the server is not multi-user.
- **State**: `_users/<id>/chat_history/product-schedules.json` holds each
  user's enable override and run bookkeeping (last run, status, counts).
  A schedule that has never run waits for its next cron occurrence rather
  than firing on first start.
- **Run history**: `schedule-runs.json` next to the product conversation
  (`_users/<id>/Chats/...`), the same file and shape workflow schedules use.
- **Execution**: one session, the product conversation's own, one
  `startSessionInternal` call per message, strictly sequential; a failing
  message stops the run. One run per (user, schedule) at a time.
- **API**: product schedules appear in `GET /api/scheduler/jobs` with
  `entity_type: "product"` and ids of the form `product:<profile>:<schedule>`.
  `GET /jobs/{id}`, `/enable`, `/disable`, `/trigger`, `/stop` and `/runs`
  work on them for the calling user. `PUT` and `DELETE` are refused: a
  product declares its schedules, users only switch them on or off.

SparkQuill's Pulse is the first schedule expressed this way (in its
standalone family server it runs through `productschedule.Runner` with the
parent's cadence settings, the quiet rule and per-check status at
`GET /api/pulse/status`); when SparkQuill becomes a hosted product its
`schedules:` block is the same definition.

## UI Surfaces

The current frontend scheduling surfaces are:

- [SchedulePresetPopup.tsx](../../frontend/src/components/SchedulePresetPopup.tsx)
- [WorkflowScheduleRunsPanel.tsx](../../frontend/src/components/scheduler/WorkflowScheduleRunsPanel.tsx)
- [scheduler.ts](../../frontend/src/api/scheduler.ts)

The UI supports:

- creating and editing workflow schedules
- selecting variable groups
- enabling and disabling schedules
- manual trigger
- stop for active sessions
- viewing schedule run history
- drilling into logs, costs, evaluation, and final output for scheduled runs
- global scheduler pause state and disabled-via-env state

## Current Architecture Summary

Use this mental model:

- `workflow.json` defines what should run and when
- `config/scheduler.json` controls whether automatic cron execution is paused or disabled on this server
- `schedule-runs.json` records what actually happened
- scheduler runtime state is mostly in memory
- scheduled execution still runs through the same workflow or workshop engines as manual execution
- in remote workspace mode, the scheduler engine is on the online local runner;
  the server gateway stores files and leases, but does not execute LLM/coding
  agents itself

Related docs:

- [workflow_manifest_architecture.md](workflow_manifest_architecture.md)
- [iteration_run_folder_architecture.md](iteration_run_folder_architecture.md)
- [workflow_builder_interactive.md](workflow_builder_interactive.md)
- [workflow_monitoring.md](workflow_monitoring.md)
- [cost_and_log_measurement.md](cost_and_log_measurement.md)
