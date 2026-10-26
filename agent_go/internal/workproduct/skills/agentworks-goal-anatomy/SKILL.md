---
name: agentworks-goal-anatomy
description: "Use when you need to know where something lives in a Goal folder: plan.json, step_config, groups and variables, iterations and runs, logs, db, costs."
---

# Anatomy of a Goal: where everything lives

A Goal is a folder (for example `Workflow/<name>`). Paths below are relative to it. Once a Goal is linked to you, read its authorized files except secret values and private chats; read its database with `database` actions `describe_linked` and `query_linked`, using the exact path from `workspace` action `references_list`. Read this before opening files, so you go straight to the right place.

## The plan and its settings
| Path | What it is |
|---|---|
| `soul/soul.md` | The Goal's purpose and success criteria. Stable; never edit it. |
| `planning/plan.json` | **The steps.** Each has `id`, `title`, `type` (`agent`, `scripted`, `routing`, ...), `description` (what to do), `validation_schema` (what its output must look like) and routes/next steps. |
| `planning/step_config.json` | Per-step overrides: model, execution mode, store access, enabled skills, locks. A step with no entry uses the defaults. |
| `planning/changelog/`, `planning/revisions/` | What changed in the plan and when. Use them to answer 'what changed between the last good run and this one'. |
| `workflow.json` | Goal-wide settings: selected servers/tools/skills, model config, schedules, run retention, notifications (including `wake_tab`), backup and publish pointers. |
| `variables/variables.json` | **Groups and variables.** A *group* is one named set of values (a segment, region, account or campaign, for example `dubai-real-estate`); the Goal runs once per group per schedule. `variables` are the names every group fills in. Disabled groups do not run. |
| `schedules/` and `schedule-runs.json` | The Goal's saved schedules and run history. Read them with `read_linked_goal` action `runs` (each timer, its on/off state and its runs); `schedules` action `list` covers this Crew's schedules only. |

## Runs: iterations and groups
`runs/<iteration>/<group>/`
- **Iteration** = one execution round. `iteration-0` is interactive Builder work; `iteration-N-sched` is a schedule's run number N; `iteration-N-hook` is a webhook's. A higher N is later. `read_linked_goal` action `runs` lists the scheduled runs with status and times; open `run_metadata.json` for one run's detail.
- **Group** = which variable set that run used. The same schedule produces one folder per group, so a Goal with six groups has six sibling folders per iteration. Compare groups to see which segment converts best, or which one keeps failing.
- `run_metadata.json`: `status`, `started_at`, `completed_at`, `duration_ms`, `trigger_source`, `plan_revision`, and `completion_receipts` per step.
- `execution/<step-id>/`: that step's output files (JSON). A routing step's `route_selection.json` says which route ran. Downloads are under `execution/Downloads/`.
- `logs/<step-id>/`: `execution/*-conversation.json` (full conversation and tool calls), `*-iteration-*.json` (model, result, duration, call counts), `*-timing.json` (where the time went: `agent.*`, `llm.*`, `tools.calls[]`), `scripted_fast_path.json` (a scripted step's exit code, stdout, error), and `pre_validation.json` (did the output pass its schema).

## Code, data and reports
| Path | What it is |
|---|---|
| `code/<step-id>/main.py` | The saved script for a scripted step; `script_metadata.json` beside it has run counts, durations, last failures and the success/failure streak. (Check `workflow.json.code_layout_version`: version 1 uses `code/`, older Goals use `learnings/`.) |
| `db/db.sqlite` | The Goal's state and results: one table per entity. `db/README.md` documents each table (columns, primary key, writers, readers). Read it first, then query. |
| `db/reports/` and `code/reports/` | Live report pages and their data scripts. |
| `db/assets/` | Durable output files that rows or reports refer to. |
| `knowledgebase/` | `context/context.md` (business context steps must respect) and `notes/*.md` (what the Goal has learned). |
| `skills/` | Goal-local how-to guides. `memory/`, `MEMORY.md`: durable notes. |
| `builder/` | Builder chat history (private to each person; read your own only). |

## Costs
- `costs/costs.sqlite` is the Goal's cost ledger. `costs` action `summary` provides available aggregates. Step-level cost queries have no Crew tool yet; use authorized saved cost reports where available until the platform adds a tool. Never open the SQLite file directly.
- `costs/execution/<group>/<date>.json` are daily token-usage shards for step execution (the bulk of a real run). `costs/phase/` covers only planning and Builder chat, so never compare it with the execution total.
- Useful questions: last 7 days vs the 7 before; which step costs most; which group; which model; cost per result (see `agentworks-review-architecture`).
- Cached tokens are charged separately (`cache_cost_usd`), so a tiny input cost next to a big token count is normal.

## Reading order for 'what went wrong in the last run'
1. `run_metadata.json` (did it finish, how long).
2. `completion_receipts`, then the failing step's `logs/<step-id>/execution/*-iteration-*.json` and `pre_validation.json`.
3. The step's `execution/<step-id>/` outputs.
4. `planning/changelog` and `plan_revision`: did the plan change since the last good run?
5. The database rows that run wrote.
