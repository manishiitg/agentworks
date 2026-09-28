**Saved-code paths:** Read `workflow.json.code_layout_version` first. In this reference, `<script-dir>` means `code/<step-id>` for version 1, or `learnings/<step-id>` for absent/zero (legacy). Resolve the placeholder before using a path; never infer the version from folders or migrate an existing workflow implicitly. Version 1 executes and repairs canonical source directly, with shared helpers under `WORKFLOW_CODE_ROOT`; only legacy workflows copy code into runs and save it back.

## FILE LAYOUT

**Shell working directory**: the absolute workspace path (e.g. `/app/workspace-docs/Workflow/<name>/`) — get the exact value from the CURRENT STATE block of your system prompt or from `AbsWorkspacePath` if available.

- Always use **absolute paths** in shell commands: prefix every path with the workspace root.
- Do **not** use `cd` or relative paths.

All paths below are relative to the workspace root (prepend the absolute root when running shell commands).

### Plan & Config
| Path | Contents |
|------|----------|
| workflow.json | Workflow-level config — selected servers/tools/skills, LLM config, schedules, retention, publish/backup pointers |
| planning/plan.json | Workflow plan — step definitions, descriptions, validation schemas |
| planning/step_config.json | Step-level config overrides (LLM, execution mode, learnings, etc.) |
| variables/variables.json | Runtime variables and groups |
| soul/soul.md | Stable workflow intent only: objective, success criteria, and optional explicit user-approved constraints. Never store notification preferences, architecture, implementation choices, or agent-inferred assumptions here; notifications belong in `workflow.json`. |

### Execution Outputs (per run, per group)
| Path | Contents |
|------|----------|
| runs/{iter}/{group}/execution/{step-id}/ | Step output files (*.json) |
| runs/{iter}/{group}/execution/{agent-step}/shared/ | Files intentionally shared by the parent Agent and its children |
| runs/{iter}/{group}/execution/{agent-step}/agents/{route-id}/session.json | Conversational specialist observability session (not resume state) |
| runs/{iter}/{group}/execution/{agent-step}/agents/{route-id}/calls/{call-id}/ | One conversational specialist invocation; this leaf is its `STEP_OUTPUT_DIR` |
| runs/{iter}/{group}/execution/{agent-step}/scripts/items/{item-id}/calls/{call-id}/ | One scripted Agent-item invocation; this leaf is its `STEP_OUTPUT_DIR` |
| runs/{iter}/{group}/execution/{agent-step}/scripts/routes/{route-id}/calls/{call-id}/ | One scripted route invocation; this leaf is its `STEP_OUTPUT_DIR` |
| runs/{iter}/{group}/execution/Downloads/ | Downloaded files (bank statements, etc.) |
| costs/execution/{group}/{YYYY-MM-DD}.json | Execution token-usage ledger shard. Current records live under `executions[execution_id]`; `run_folder` is display metadata and `run_folders` is legacy compatibility only. |
| costs/evaluation/{group}/{YYYY-MM-DD}.json | LEGACY (retired): evaluation cost ledger. Retained on disk; nothing writes or reads it. |
| costs/phase/token_usage.json | Token usage for the `planning` phase plus workflow-builder chat interactions ONLY — not a workflow-wide total. Step-execution cost (the bulk of a real run) lives in `costs/execution/`, not here. `input_cost_usd` excludes cache-served tokens by design (`cache_cost_usd` carries their real charge) — a near-zero `input_cost_usd` next to a large `input_tokens` count is expected for a cache-heavy workload, not a pricing defect. |
| costs/phase/daily/{YYYY-MM-DD}.json | Same narrow phase/model-key scope as `costs/phase/token_usage.json`, rolled up by date. Do not compare its total against `costs/execution/` and infer under/over-counting — the two ledgers cover different, non-overlapping call sets by design. |
| costs/costs.sqlite | This workflow's own cost ledger (PLAT-184) — per-run, per-step `phase`, and per-message-sequence-item (`phase="item:<id>"`) cost/token breakdown for every LLM and paid-tool call attributed to this workflow. Query it with `query_workflow_costs`, never by reading the file directly. It only holds events recorded since PLAT-184 shipped — it is not a backfilled history and not the same store as the global human-facing Cost Analysis dashboard, which is a separate, workflow-agent-unreachable ledger. |

An Agent and its children share the Agent step subtree. Do not author references
to retired flattened `step-*-sub-*`, `step-*-generic-*`,
`execution/message_sequences/`, or `scripts/<item>/<call>/` paths. Current
script source is `code/<script-step-id>/main.py`; its working directory is
`code/<script-step-id>/`, independent of `STEP_OUTPUT_DIR`.

### Execution Logs (per run, per group, per step)
| Path | Contents |
|------|----------|
| runs/{iter}/{group}/run_metadata.json | **Workflow-level timing**: `started_at`, `completed_at`, `duration_ms`, `status` |
| runs/{iter}/{group}/logs/{step-id}/execution/*-conversation.json | Full conversation log: `conversation_history` (messages) + `tool_calls[]` (each with `tool_name`, `args`, `result`, `duration`) |
| runs/{iter}/{group}/logs/{step-id}/execution/*-iteration-*.json | Execution summary: model, result text, step path, `duration_ms`, `llm_call_count`, `llm_duration_ms`, `tool_call_count`, `tool_duration_ms` |
| runs/{iter}/{group}/logs/{step-id}/execution/*-timing.json | **Clear timing breakdown**: read `agent.*` for agent wall-clock, `llm.*` for LLM timing (`time_to_first_response_ms`, `time_to_first_content_ms`, `time_to_first_tool_call_ms`), and `tools.calls[]` for per-tool durations/offsets |
| runs/{iter}/{group}/logs/{step-id}/execution/scripted_fast_path.json | **scripted steps**: main.py result — `exit_code`, `output` (stdout), `error`, `success`, `script_path` |
| runs/{iter}/{group}/logs/{step-id}/pre_validation.json | Pre-validation result: `overall_pass`, `errors[]`, `files_checked[]`, `schema_used` |

### Best Way To Read Timing

Use this order when debugging latency:

1. Read `run_metadata.json` first to get the total workflow wall-clock and whether the run finished or failed.
2. Read each step's `execution-attempt-{N}-iteration-{M}.json` next to rank slow steps quickly using `duration_ms`, `llm_duration_ms`, and `tool_duration_ms`.
3. Open the matching `execution-attempt-{N}-iteration-{M}-timing.json` for the slowest step.
4. In that timing file, interpret fields in this order:
   - `agent.duration_ms` = full wall-clock time for the step attempt.
   - `llm.total_duration_ms` = total time spent waiting on LLM calls across the attempt.
   - `llm.time_to_first_response_ms` = delay before the model produced its first visible response signal.
   - `llm.time_to_first_content_ms` = delay before the first text content arrived.
   - `llm.time_to_first_tool_call_ms` = delay before the model decided to invoke a tool.
   - `tools.total_duration_ms` = total time spent inside tools.
5. Use `llm.calls[]` to see whether one LLM call dominated latency or whether many smaller calls accumulated.
6. Use `tools.calls[]` to find the exact slow tool. Prefer `duration_ms` for cost/time ranking and `offset_from_agent_start_ms` to understand when it happened inside the step.
7. If `agent.duration_ms` is much larger than both `llm.total_duration_ms` and `tools.total_duration_ms`, infer the remaining gap is orchestration overhead, prompt construction, validation, file IO, or other non-LLM/non-tool work.
8. Use the conversation log only after timing isolation, to explain *why* the slow LLM/tool call happened rather than to discover *which* one was slow.

### Learnings (persistent across runs)
| Path | Contents |
|------|----------|
| <script-dir>/main.py | **scripted steps**: saved Python script — executed on each scripted run via fast path |
| learnings/_global/SKILL.md | Global prose learnings shared across all steps |
| <script-dir>/script_metadata.json | Script version, run counts, per-group stats, duration stats, recent run history (last 10 with exit codes/errors/durations), last failure details, success/failure streak |

### Legacy evaluation (retired, retained on disk, no longer read)
| Path | Contents |
|------|----------|
| evaluation/evaluation_plan.json | Retired eval step definitions |
| evaluation/runs/{iter}/{group}/evaluation_report.json | Retired eval step outputs + evidence |

### Other
| Path | Contents |
|------|----------|
| builder/conversation/users/{user-id}/YYYY-MM-DD/session-{id}-conversation.json | Current user's previous builder chat sessions. Its `conversation_history` holds the latest messages (`history_tail: true`, `history_total` = all); the full history is `session-{id}-conversation.history.jsonl` beside it, one JSON message per line |
| db/db.sqlite | Workflow state and results — one SQLite database, one table per entity (agentic steps use managed DB tools; saved scripts retain direct compatibility; upsert on the primary key) |
| db/README.md | Per-table schema contract (DDL, primary key, upsert rule, indexes, writers, consumers) |
| db/assets/* | Durable output media/files referenced by db rows, reports, or later steps. Source belongs in the manifest-selected script directory, not here. Version 1 unlocked steps also have workflow code-tree write access; other writes remain subject to the current Folder Guard grants. |
| db/reports/*.html | Workflow-owned live report documents; index.html is default, each owns its internal layout and reads db/db.sqlite through window.report |
| code/reports/*.py | Report data scripts called by Dashboards through `window.report.run` (live outside data; read-only, one JSON value on stdout; see reporting-policy) |
| .report-cache/ | Platform-managed, the only folder report scripts may write (`$REPORT_CACHE_DIR`); disposable, shared by all viewers |
| db/reports/views.json | Optional toolbar metadata: stable view IDs, titles, order, and default document |
| knowledgebase/context/context.md | User-supplied runtime business context that steps with KB read access must respect |
| knowledgebase/notes/*.md | Per-topic narrative markdown — durable observations discovered by the workflow. Normally written by step agents in direct-write mode; post-step KB agent only when explicitly requested. |
| knowledgebase/notes/_index.json | Topic registry (covers, size_bytes, section_count, last_updated) kept in sync with notes/*.md |

### Outside The Workflow Root

| Path | Contents |
|------|----------|
| skills/<folder>/SKILL.md | Installed workspace skills shared by all workflows; workflow.json records selected skills, and planning/step_config.json records per-step enabled_skills |

**Cleanup**: Delete old builder conversation files when >3 exist (`find builder/conversation/users -name 'session-*-conversation.json'`, keep latest; delete each one's `.history.jsonl` with it). Never edit these files by hand. Other users' chat folders are private and blocked by the folder guard.
