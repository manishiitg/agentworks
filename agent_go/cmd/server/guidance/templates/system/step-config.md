---
name: "step-config"
description: "Per-step config reference: store-access modes, locks, execution mode, model selection, validation_schema, skills, clearing fields. Load before tuning a step."
external: true
external_tools: ["settings"]
external_note: "External mapping: discover current tool schemas before acting. Use plan action=list for native plan and step operations; read get_plan for expected_revision, then pass a stable request_id and listed native arguments. Internal operation names are plan action values, not standalone external tools. Settings uses settings get/update. For skill wiring, use list_workflow_knowledge and read_workflow_knowledge, Crew package actions and Brain skill packages under their existing roles and file/folder grants. Fetch referenced guidance by its listed topic name. Server-only scripts, bridge variables and internal URLs are not external client credentials."
---
**Plan-editing tool arguments:** Before a plan mutation, read `builder-reference/references/plan-editing-tools.md`. Step fields described below belong inside `add_step.step` or `update_step.changes`; route/group/maintenance fields belong inside `parameters`. Use the live type/action-specific schema; these field descriptions do not authorize flat arguments or extra fields.

**Saved-code paths:** Read `workflow.json.code_layout_version` first. In this reference, `<script-dir>` means `code/<step-id>` for version 1, or `learnings/<step-id>` for absent/zero (legacy). Resolve the placeholder before using a path; never infer the version from folders or migrate an existing workflow implicitly. Version 1 executes and repairs canonical source directly, with shared helpers under `WORKFLOW_CODE_ROOT`; only legacy workflows copy code into runs and save it back.

## STEP CONFIG — planning/step_config.json (per-step tuning)

Every step has an optional config entry that overrides defaults for that step. All of it is set with **`update_step_config(step_id, ...)`** and removed with **`update_step_config(step_id, clear=[...])`** (clearing returns the field to its default). Never hand-edit `planning/step_config.json`. This doc is the one-stop map of the knobs; load the linked deep-dives when a knob needs more than the summary.

### Knowledge-base access and skills

A step's access to each store is independent and defaults differently. Grant the least access the step actually needs.

| Field | Values | Default | Grant when… |
|---|---|---|---|
| ~~`db_access`~~ | **retired** | `read-write` | Had no runtime effect — `resolveDBAccess` ignored it and always granted read-write, while the enum invited agents to "restrict" a step that never was restricted. Removed in PLAT-061; existing values still parse and are ignored. Every workflow step gets managed read-write DB access. |

- **DB contract:** every agentic workflow step receives `database action=query` and `database action=mutate`; it does not receive raw SQLite access. Scripted code reaches the database through the built-in `agentworks_db` helper (the same managed tools); `$DB_PATH` remains only for scripts written before contract 1.0.45 and is not to be used in new or repaired scripts.
- Deep dive on what belongs in each store and the write contracts: `read_skill(skills=[{"name":"builder-reference","path":"references/stores.md"}])`.

| `knowledgebase_access` | `read` · `write` · `read-write` · `none` | `read` | Permits task-directed notes contributions. Preserve the mode when migrating legacy objectives. |

### The per-step code lock

| Lock | Scope | Effect | Set when |
|---|---|---|---|
| `lock_code` | per-step (scripted) | Freezes `<script-dir>/main.py` even for the Builder's own `execute_step`. Runs never repair a script regardless: a failing script fails the step and AgentWorks reports it | **user asks to lock** → allow it; **Workshop auto-locking on its own** → only after 10+ scenario-covering runs |

Knowledge-base writes are controlled per step instead: `knowledgebase_access`
must permit writes and the task must state what durable facts the step owns. Change a mature/no-op contributor to `read`; do not freeze
unrelated KB writers workflow-wide.

Only pass `lock_code` when explicitly changing it. Steps read selected skills; Builder maintain skill files. Contract 1.0.51 removes `learnings_access` from new authoring and preserves old grants in migration audit notes without changing KB access.

### Execution mode + which model runs the step

- **Execution mode is the step's plan type, not a config field**: `scripted` = scripted (a checked-in `<script-dir>/main.py`, created with `add_step`), `agent` = an agentic conversation with authored turns and deterministic gates/batches. A `scripted` item runs saved scripts without a child LLM; script definitions own their permissions and validation. See `references/agent.md`. Choose the execution model when creating the step. Two different paths:
  - **Scripts are the default for DETERMINISTIC execution** — fixed API/SDK calls, CLI commands, known pagination, data fetching, stable parsing/normalization, math, data transforms, mechanical persistence, and fixed SQL. Create these as `scripted` steps from initial design and author/test `main.py`; no prior run count is required. Give them typed `script_parameters` for whatever an agent may need to vary (IDs, windows, filters) so agent steps can reuse them as tools; keep fully fixed work (zero agent decisions) a pure script without invented parameters. See `references/scripted.md`. If behavior **varies run-to-run or needs adaptive judgment** (most browser/UI flows, LLM reasoning, fuzzy extraction, live discovery), keep it an `agent`.
  - **Use coherent scripted fetchers, not micro-scripts** — batch related calls and transforms when they share one source/auth/retry/output contract. Do not create one scripted step per endpoint, command, or tiny transformation, and do not put an entire branching business workflow into one script. The usual architecture is scripted fetcher(s) → durable DB/file output → one large agentic agent.
  - **User explicitly asks for a scripted step** → create it with `add_step(type="scripted", step=...)`, then author and test its saved script. Explain any concrete limitation once and honor the request.
  - **Workshop mode selection** → create deterministic API, CLI and data work as `scripted` steps. The representative-run threshold applies before freezing code with `lock_code`, not before choosing scripted.
- **`use_code_execution_mode`**: per-step override of the preset's code-execution toggle (nil = inherit).
- **Model selection**: `execution_tier` (`high`/`medium`/`low`) maps to the workflow's tiered allocation; `execution_llm` / `validation_llm` pin a specific published model for that role. Prefer tiers over hard pins, and prefer leaving the tier unset over pinning it. Full framework: `read_skill(skills=[{"name":"builder-reference","path":"references/llm-selection.md"}])`.

### Architecture-owned decisions need a stated reason (PLAT-060)

Two fields are cost decisions owned by the `architecture_review` tier/model focus, and each **requires a
paired reason — `update_step_config` rejects the change without it**:

| Field | Required reason | The consequence the reason must acknowledge |
|---|---|---|
| `execution_tier` | `execution_tier_reason` | The configured tier remains in force until explicitly changed; run counts never change it |
| `execution_llm` | `execution_llm_reason` | A pin **outranks `execution_tier` entirely** and will not follow provider-profile updates |

Cite the owning `architecture_review` finding id, the current state, and the evidence
— and the `human_input_id` when the change was user-approved. Clearing a field
clears its reason; clearing never requires one.

**If the evidence does not settle it, do not make the change.** Raise a decision
with `create_human_input_request` and park the finding `awaiting_user`.
Uncertainty is a legitimate terminal state; an invented reason is worse than no
change, because a fabricated justification is harder to challenge later than a
missing one.

Legacy `learning_objective` and `knowledgebase_contribution` are migration
inputs, not new authoring settings. Contract 1.0.50 archives their original text
and destinations; put required contributions in the task description instead.

### Other common fields

- **`validation_schema`** — the only automated gate (set via `update_validation_schema`); catches stale files, missing fields, constraint violations. Every step needs one.
- **`enabled_skills`** — step-level skill selection (step execution does NOT inherit workflow-selected skills; set explicitly). `enabled_custom_tools`, `selected_servers`, `selected_tools` — narrow the step's tool surface. Human tools are not baseline: when a step sends email, posts to Slack or asks a person for a decision, name `human_tools:send_email`, `human_tools:send_slack_message` or `human_tools:create_human_input_request` in its `enabled_custom_tools` (keep the existing entries); never rely on the description's wording. `human_feedback` blocks a run, so unattended steps do not get it.
- **`additional_read_paths`** — not needed for files inside the workflow: a step can already READ everything in its workflow folder (every run, code, knowledge base, skills, db files, notes, reports). Do not add it for those reads. It remains for read-only grants outside the workflow folder's usual surfaces that a product defines. Absolute paths, `.` and `..` traversal are rejected, and the grant never expands writes. A step WRITES only its current run folder and the shared areas db/assets, reports and tool_output_folder (plus its own knowledge base and database access); never past runs, planning, settings, skills or code, which belong to the Builder and AgentWorks.
- **`review_notes`** — one-line WHY for non-obvious config (future AgentWorks and Workshop reviews read it). Record it whenever you set KB access or skill selections or designate a db writer.
- **`description_reviewed`**, `coding_agent_tmux_lifecycle`, `disable_parallel_tool_execution` — situational. (`transport` was never a step-config field; naming it in `clear_fields` is reported as a no-op.)

### Workflow

1. `update_step_config(step_id, <field>=<value>, ...)` to set; `update_step_config(step_id, clear=["<field>", ...])` to revert to default.
2. Match access grants to explicit task contributions; no separate objective or hidden turn is required.
3. For the reliability/strategy decision-making that drives most config changes: `read_skill(skills=[{"name":"builder-reference","path":"references/optimize-playbook.md"}])`.

## Runtime procedures and tools

Read `references/step-description.md` for the runtime skill/tool baseline. Keep task/domain skills selected for their actual use; load runtime procedures on demand rather than copying them into each step.
