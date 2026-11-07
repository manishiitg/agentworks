# Workflow Builder Commands And Tools

This document is a compact reference for the current workflow-builder surface. The canonical slash-command prose lives in `agent_go/cmd/server/guidance/templates/`; do not duplicate those templates here.

## Core Model

Workflow improvement has three layers:

- **Intent + plan**: `soul/soul.md` defines stable objective/success criteria and explicit user constraints; `planning/plan.json` defines the current, revisable implementation attempt.
- **Goal + progress**: `soul/soul.md` is the durable Goal / Ikigai definition and Runloop renders it directly. Evidence-stamped progress over time lives in `builder/improve.html` Reflection entries; there is no separate numeric metrics layer or duplicate Goal/Profile card.

Optimizer actions are deliberately small in number:

- Measurement improvement: use when measurement coverage, scoring, structured output, or validation schema is weak enough that measurement cannot be trusted, or measurement cost is out of proportion to run cost.

All plan/design reviews and KB/learnings/DB/eval/report/workflow improvement commands load the shared `assumption-audit` reference. It prevents repeated agent-written choices from becoming accidental constraints: explicit user constraints and verified external facts are preserved; current design choices remain revisable; unsupported assumptions are corrected within command boundaries or surfaced once under an owner decision with concrete evidence.

## Workshop Modes

- `builder`: creates and debugs workflow structure. Builder defaults steps to `code_exec`; learn-code promotion belongs to Optimizer only after explicit user request, deterministic behavior, and 10+ scenario-covering successful runs.
- `optimizer`: improves existing workflows from `runs/iteration-0`, eval reports, logs, and `builder/improve.html`.
- `run`: user-facing runtime for Slack/WhatsApp and normal operation. It can answer directly from workflow state, read KB/learnings/db/run artifacts, execute normal or orphan utility steps, or run the full workflow. It should not mutate plan/config/eval/report definitions; durable user-owned runtime context is captured through `capture_context`.
- Reporting authoring is available in Builder and Optimizer through report-plan tools. The legacy Reporting mode remains for compatibility.

## Guidance Tool

Slash commands are one-line UI shortcuts that call:

```text
get_workflow_command_guidance(kind="...", focus?)
```

The returned guidance is the source of truth for the command. Mode validation lives in the guidance registry. Slash-command callers should pass the conversation or request text before the slash command as `focus`, so guidance can apply the user's recent constraints and "based on what we just discussed" intent.

Current guidance kinds:

```text
design-plan
run-plan-drift
design-dashboard
```

## Key Slash Commands

| Command | Mode | Purpose |
|---|---|---|
| `/design-plan` | Workshop | Comprehensive structural, artifact, and design-quality review. |
| `/run-plan-drift` | Workshop | Check the plan still fits its steps, reports and schedules; apply bounded safe repairs. |
| `/design-dashboard` | Workshop | Design the workflow's dashboard from its real data. |

Other product commands (`/run`, `/fix`, `/schedule`, `/share`, `/connect`, `/notify`, `/upgrade`) are prompt-only files under `agent_go/internal/agentworksproduct/commands/`.

## Common Tool Groups

| Area | Tools |
|---|---|
| Execution | `execute_step`, `query_step`, `send_step_message`, `stop_step`, `stop_all_executions`, `list_executions`, `run_full_workflow`, `debug_step` |
| Plan/config | `add_step` (agent/scripted/routing/branch), `update_step`, `delete_plan_steps`, `cleanup_orphan_step_configs`, `update_step_config`, `update_validation_schema` |
| Review | `review_plan`, `review_workflow_timing`, `review_workflow_costs`; artifact drift uses `/run-plan-drift` with `call_generic_agent` |
| Reports | `get_report_plan`, `upsert_report_widget`, `move_report_widget`, `toggle_report_widget`, `remove_report_widget`, `set_report_theme`, `set_section_layout`, `validate_report_plan`, `preview_report_render` |
| Schedules | `create_schedule`, `create_calendar_schedule`, `update_schedule`, `delete_schedule`, `trigger_schedule`, `get_schedule_runs` |

## Continuous Improvement Cadence

