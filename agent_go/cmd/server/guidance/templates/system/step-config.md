**Plan-editing tool arguments:** Before a plan mutation, read `builder-reference/references/plan-editing-tools.md`. Step fields described below belong inside `add_step.step` or `update_step.changes`; route/group/maintenance fields belong inside `parameters`. Use the live type/action-specific schema; these field descriptions do not authorize flat arguments or extra fields.

**Saved-code paths:** Read `workflow.json.code_layout_version` first. In this reference, `<script-dir>` means `code/<step-id>` for version 1, or `learnings/<step-id>` for absent/zero (legacy). Resolve the placeholder before using a path; never infer the version from folders or migrate an existing workflow implicitly. Version 1 executes and repairs canonical source directly, with shared helpers under `WORKFLOW_CODE_ROOT`; only legacy workflows copy code into runs and save it back.

## STEP CONFIG — planning/step_config.json (per-step tuning)

Every step has an optional config entry that overrides defaults for that step. All of it is set with **`update_step_config(step_id, ...)`** and removed with **`update_step_config(step_id, clear=[...])`** (clearing returns the field to its default). Never hand-edit `planning/step_config.json`. This doc is the one-stop map of the knobs; load the linked deep-dives when a knob needs more than the summary.

### Store access — who can touch the three persistent stores

A step's access to each store is independent and defaults differently. Grant the least access the step actually needs.

| Field | Values | Default | Grant when… |
|---|---|---|---|
| `learnings_access` | `read` · `read-write` · `none` | `read` | `read-write` only for reusable execution HOW (browser selectors/timing/auth, API/MCP quirks, CLI/SDK patterns, parsing/retry/recovery) — also requires a concrete `learning_objective`. `none` only when shared SKILL.md would mislead the step. |
| `knowledgebase_access` | `read` · `write` · `read-write` · `none` | `read` | Since PLAT-055/K, unset now defaults to `read` (mirrors `learnings_access` — every step sees KB context/notes unless told otherwise) and auto-promotes to `read-write` if a `knowledgebase_contribution` is already staged. WRITE stays a deliberate opt-in: set `write`/`read-write` plus a non-empty `knowledgebase_contribution`. Set `none` explicitly only when KB would mislead the step or a routing step needs a lean prompt — an explicit `none` always wins over the default. |
| ~~`db_access`~~ | **retired** | `read-write` | Had no runtime effect — `resolveDBAccess` ignored it and always granted read-write, while the enum invited agents to "restrict" a step that never was restricted. Removed in PLAT-061; existing values still parse and are ignored. Every workflow step gets managed read-write DB access. |

- **DB contract:** every agentic workflow step receives `query_workflow_db` and `mutate_workflow_db`; it does not receive raw SQLite access. Scripted code reaches the database through the built-in `agentworks_db` helper (the same managed tools); `$DB_PATH` remains only for scripts written before contract 1.0.45 and is not to be used in new or repaired scripts.
- **Rule of thumb:** routing, validation, mechanical transforms, aggregation/report-shaping, human approval, pure db/KB readers, and mature scripted steps should usually stay read-only on learnings. DB access is intentionally uniform and is not a per-step tuning decision right now.
- Deep dive on what belongs in each store and the write contracts: `read_skill(skills=[{"name":"builder-reference","path":"references/stores.md"}])`.

### The per-step code lock

| Lock | Scope | Effect | Set when |
|---|---|---|---|
| `lock_code` | per-step (scripted) | Freezes `<script-dir>/main.py` even for the Builder's own `execute_step`. Runs never repair a script regardless: a failing script fails the step and Pulse reports it | **user asks to lock** → allow it; **Workshop auto-locking on its own** → only after 10+ scenario-covering runs |

Knowledge-base writes are controlled per step instead: `knowledgebase_access`
must permit writes and `knowledgebase_contribution` must state what durable
facts the step owns. Change a mature/no-op contributor to `read`; do not freeze
unrelated KB writers workflow-wide.

Only pass `lock_code` when explicitly changing it. Learning write eligibility is controlled only by `learnings_access`: use `read` to consume guidance without contributing.

### Execution mode + which model runs the step

- **Execution mode is the step's plan type, not a config field**: `regular` = scripted (a checked-in `<script-dir>/main.py`, created with `add_step`), `agent` = an agentic conversation with authored turns and deterministic gates/batches. A `scripted` item runs saved scripts without a child LLM; script definitions own their permissions and validation. See `references/agent.md`. Move an existing step between them with `change_step_type(step_id, target_type="scripted"|"agent", reason)`. Two different paths, don't conflate them:
  - **Scripts are the default for DETERMINISTIC execution** — fixed API/SDK calls, CLI commands, known pagination, data fetching, stable parsing/normalization, math, data transforms, mechanical persistence, and fixed SQL. Create these as `regular` steps from initial design and author/test `main.py`; no prior run count is required. Give them typed `script_parameters` for whatever an agent may need to vary (IDs, windows, filters) so agent steps can reuse them as tools; keep fully fixed work (zero agent decisions) a pure script without invented parameters. See `references/scripted.md`. If behavior **varies run-to-run or needs adaptive judgment** (most browser/UI flows, LLM reasoning, fuzzy extraction, live discovery), keep it a `agent`.
  - **Use coherent scripted fetchers, not micro-scripts** — batch related calls and transforms when they share one source/auth/retry/output contract. Do not create one scripted step per endpoint, command, or tiny transformation, and do not put an entire branching business workflow into one script. The usual architecture is scripted fetcher(s) → durable DB/file output → one large agentic agent.
  - **User explicitly asks for a scripted step** (e.g. "make this scripted so I can test it") → `change_step_type(step_id, target_type="scripted", reason=...)` right away — the user owns that call; **no run-count gate**. But if the work isn't deterministic, say so plainly first ("this flow reads live UI state, so a frozen script will break often — agentic is more reliable; want me to script it anyway?") and honor their decision.
  - **Workshop mode selection** → create obviously deterministic API/CLI/data work as `regular` steps, or move it there with `change_step_type`, immediately. The 10+ representative-run threshold applies only before freezing it with `lock_code`, not before choosing scripted.
  - The `reason` passed to `change_step_type` is the audit trail; it is recorded in the plan changelog.
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

By contrast `learning_objective` and `knowledgebase_contribution` are **not**
gated — they are expected to be refined continuously as evidence accumulates,
and a write-time gate would suppress exactly that iteration. Record the why in
`review_notes`.

### Other common fields

- **`validation_schema`** — the only automated gate (set via `update_validation_schema`); catches stale files, missing fields, constraint violations. Every step needs one.
- **`enabled_skills`** — step-level skill selection (step execution does NOT inherit workflow-selected skills; set explicitly). `enabled_custom_tools`, `selected_servers`, `selected_tools` — narrow the step's tool surface.
- **`additional_read_paths`** — narrowly grant declared workflow-relative inputs outside the standard execution/db/KB/learnings surfaces (for example `["variables"]`). It is read-only. Absolute paths, `.` and `..` traversal are rejected, and the grant never expands writes.
- **`review_notes`** — one-line WHY for non-obvious config (future Pulse and Workshop reviews read it). Record it whenever you set learning/KB access or designate a db writer.
- **`description_reviewed`**, `coding_agent_tmux_lifecycle`, `disable_parallel_tool_execution` — situational. (`transport` was never a step-config field; naming it in `clear_fields` is reported as a no-op.)

### Workflow

1. `update_step_config(step_id, <field>=<value>, ...)` to set; `update_step_config(step_id, clear=["<field>", ...])` to revert to default.
2. Pair access grants with their prerequisite (`learnings_access: read-write` ⇒ `learning_objective`; KB write ⇒ `knowledgebase_contribution`).
3. For the reliability/strategy decision-making that drives most config changes: `read_skill(skills=[{"name":"builder-reference","path":"references/optimize-playbook.md"}])`.
