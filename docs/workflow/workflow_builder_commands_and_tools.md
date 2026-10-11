# Workflow Builder Commands And Tools

This document is a compact reference for the current workflow-builder surface. The canonical slash-command prose lives in `agent_go/cmd/server/guidance/templates/`; do not duplicate those templates here.

## Core Model

Workflow improvement has three layers:

- **Intent + plan**: `soul/soul.md` defines stable objective/success criteria and explicit user constraints; `planning/plan.json` defines the current, revisable implementation attempt.
- **Measurement**: run-scoped, evidence-backed outcomes stored by the producing steps, plus goal observations for Pulse history. This measures success-criteria achievement (operational correctness is Pulse Bug Review's job).
- **Goal + progress**: `soul/soul.md` is the durable Goal / Ikigai definition and Runloop renders it directly. Evidence-stamped progress over time lives in `builder/improve.html` Reflection entries; there is no separate numeric metrics layer or duplicate Goal/Profile card.

Optimizer actions are deliberately small in number:

- `Pulse Bug Review/Fixer(group_name?, focus?)`: use when the workflow path is basically right, but prompts, config, validation, KB, learnings, db/report wiring, or measurement coverage need repair. It should delete stale `learnings/{step-id}/main.py` for `code_exec` steps and only patch `main.py` for `learn_code`.
- Goal Advisor proposals and experiments: use for Goal recovery, a capped strategy, or periodic healthy 10x/headroom review. Scheduled Pulse selects it inside the normal Review+Fix conversation; material plan changes are proposed through `create_human_input_request(source="goal_advisor", ...)` and later applied with normal plan/config/measurement/report tools only after approval. `builder/improve.html` holds at most one active `.advisor-experiment` card, which preserves the current baseline and advances through proposal, approval, running, measurement, and a terminal adopted/rejected/retired outcome. Pulse schedules the next meaningful checkpoint instead of generating bold ideas every run.
- Measurement improvement: use when measurement coverage, scoring, structured output, or validation schema is weak enough that measurement cannot be trusted, or measurement cost is out of proportion to run cost.

All plan/design reviews and KB/learnings/DB/eval/report/workflow improvement commands load the shared `assumption-audit` reference. It prevents repeated agent-written choices from becoming accidental constraints: explicit user constraints and verified external facts are preserved; current design choices remain revisable; unsupported assumptions are corrected within command boundaries or surfaced once under Pulse's **Assumptions challenged** for Goal Advisor/user judgment.

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
ready-to-optimize
ops-review
review-code
review-artifact-drift
improve-knowledge
improve-learnings
improve-data
define-success
auto-improve
improve-report
```

## Key Slash Commands

| Command | Mode | Purpose |
|---|---|---|
| `/design-plan` | Workshop, Run | Comprehensive structural, artifact, and design-quality review through the read-only `review_plan` engine. |
| `/ready-to-optimize` | Builder | Check whether the workflow is ready to hand to Optimizer. |
| `/review-code` | Optimizer | Review all saved code artifacts, including learn-code scripts. |
| `/review-artifact-drift` | Builder, Optimizer | Audit whether learnings, code, KB, db, reports, and measurement wiring drifted from recent plan changes; persist the review and open concerns into Pulse SQLite state. |
| `/bug-review` | Workshop | Run Pulse QA/logic review and persist the full review plus trackable concerns for the Pulse popup. |
| `/ops-review` | Workshop | Agentically review cost, timing, tool/runtime reliability, model routing, setup, and plan-design hygiene; persist the full review plus trackable concerns. |
| `/engineering-review` | Workshop | Run Engineering and LLM/Ops review, consolidate the findings, then apply and verify bounded fixes in one agent sequence. |
| `/strategy-auditor` | Workshop | Diagnose plan-versus-goal strategy using cross-run evidence and persist the review plus trackable concerns. |
| `/goal-advisor` | Workshop | Run the native Advisor → Critic → Finalizer pipeline and persist the complete result plus remaining open concerns. |
| `/specialize-advisors` | Workshop | Propose two reusable workflow-specific lenses—one for Strategy Auditor and one for Goal Advisor—and create an Activate / Revise / Reject decision. Activation is approval-gated and stored in `workflow.json`. |
| `/improve-knowledge` | Builder, Optimizer | Improve knowledgebase notes with targeted cleanup or cross-step consolidation. |
| `/improve-learnings` | Builder, Optimizer | Improve global learnings with targeted cleanup or current-plan consolidation. |
| `/improve-data` | Builder, Optimizer | Improve durable data contracts, schemas, and report compatibility. |
| `/define-success` | Workshop | Confirm and normalize the durable Goal / Ikigai in `soul/soul.md`; do not seed a duplicate Goal card. |
| `/auto-improve` | Optimizer | Create/update frequent Run-mode and Optimizer-mode schedules. |
| `/improve-report` | Builder, Optimizer | Improve report layout, color, density, and widget/data wiring. |

## Common Tool Groups

| Area | Tools |
|---|---|
| Execution | `execute_step`, `query_step`, `send_step_message`, `stop_step`, `stop_all_executions`, `list_executions`, `run_full_workflow`, `debug_step` |
| Plan/config | `add_scripted_step`, `add_agent_step`, `add_routing_step`, `add_human_input_step`, `add_todo_task_step`, `update_*_step`, `delete_plan_steps`, `cleanup_orphan_step_configs`, `update_step_config`, `update_validation_schema` |
| Review | `review_plan`, `review_workflow_timing`, `review_workflow_costs`; artifact drift uses `/review-artifact-drift` with `call_generic_agent` |
| Optimizer | `Pulse Review+Fix`, Goal Advisor proposal cards |
| Reports | `get_report_plan`, `upsert_report_widget`, `move_report_widget`, `toggle_report_widget`, `remove_report_widget`, `set_report_theme`, `set_section_layout`, `validate_report_plan`, `preview_report_render` |
| Schedules | `create_schedule`, `create_calendar_schedule`, `update_schedule`, `delete_schedule`, `trigger_schedule`, `get_schedule_runs` |

## Continuous Improvement Cadence

Pulse is the single recurring maintenance loop. Its Gate decides which review modules are due, and its parent Pulse Fixer applies bounded verified changes.
