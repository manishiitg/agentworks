**Plan-editing tool arguments:** Before a plan mutation, read `builder-reference/references/plan-editing-tools.md`. Step fields described below belong inside `add_step.step` or `update_step.changes`; route/group/maintenance fields belong inside `parameters`. Use the live type/action-specific schema; these field descriptions do not authorize flat arguments or extra fields.

**Saved-code paths:** Read `workflow.json.code_layout_version` first. In this reference, `<script-dir>` means `code/<step-id>` for version 1, or `learnings/<step-id>` for absent/zero (legacy). Resolve the placeholder before using a path; never infer the version from folders or migrate an existing workflow implicitly. Version 1 executes and repairs canonical source directly, with shared helpers under `WORKFLOW_CODE_ROOT`; only legacy workflows copy code into runs and save it back.

## OPTIMIZATION GUIDELINES

**Important**: For proactive optimization suggestions (learning config, server scoping, description refinement), wait until a step has had a few successful runs before pushing changes. But for **debugging failures** — when a step produces wrong output or doesn't do what it should — investigate and fix immediately, don't wait.

**Repair in service of the goal, across the whole plan.** Every fix exists to make the workflow achieve its objective — read `soul/soul.md` (success criteria) and the plan before changing behavior, understand each step's role (what feeds it, what depends on its output), and fix it so it does its real job *in that context*, not merely so it stops erroring or passes validation. A green-but-goal-empty fix (a masking default, loosened validation that lets wrong data through) is a **false fix**. If a contained, plan-coherent repair cannot make a step reliably do its job, return a **Goal finding for Goal Advisor** instead of stretching a local repair into strategy work.

When helping users optimize steps, follow these principles:

### 1. Validation Schema vs Success Criteria — They Serve Different Purposes

**validation_schema** is the deterministic output gate. It runs code-based checks with no LLM involved; a failed check follows the step's configured retry/failure policy. Passing validates the checked output, not human approval or proof of every semantic claim. Keep the gate focused on the downstream contract (see `references/step-description.md`):
- **File existence**: Output files must exist
- **Field completeness**: Require the fields consumers actually depend on. For a login check, validate the observed account identity if it matters to the next action; do not require unrelated identity fields simply to enlarge the schema
- **Value constraints**: Types, min/max lengths, regex patterns for format validation, min/max values for numbers
- **Cross-field consistency**: Use "consistency_check" to compare related fields (e.g., array length matches a count field)
- **Anti-staleness**: Check relevant run/source identity and authoritative read-back evidence. Extra field names or non-empty strings alone cannot distinguish a stale or fabricated result from a real observation.

Step-level `success_criteria` is no longer part of the recommended step design. Put semantic completion guidance into `description`, and put machine-checkable requirements into `validation_schema`.
- **validation_schema**: Check login_status.json has login_success=boolean, pan=string, dashboard_url=string (pattern: /dashboard/), account_name=string (min_length: 1)

If a step needs **semantic/LLM-based validation** (e.g., "verify the summary is accurate"), keep it in the same shared context: use or convert to one large `agent`, add a focused user-message turn that re-opens the real evidence and proves the criteria, then a repair/double-check turn. Keep machine-checkable proof/provenance in the top-level `validation_schema`. Add a separate validation step only for genuine clean-room independence, different permissions/tools, or an independently rerunnable artifact/failure domain.

When reviewing a successful step, check whether its evidence establishes freshness and authenticity. Add targeted provenance checks or semantic evidence verification where needed; do not treat an exhaustive schema as proof.

### 2. Learning Configuration

The learning system has **one active permission control** per step: `learnings_access` controls read/write scope. New plans should omit the retired `learnings_write_method` compatibility field.

- **Default access is `"read"`** (inferred when `learnings_access` is unset). Every step — including simple plumbing — sees `_global/SKILL.md` in its prompt for cross-step context. Do NOT set `learnings_access: "none"` on plumbing steps just because they don't contribute; they still benefit from reading.
- **Opt into writing** by setting `learnings_access: "read-write"` AND a non-empty `learning_objective`. Required only for steps that produce durable HOW-knowledge: browser selectors/timing/auth flows, API/MCP request/response quirks, CLI/SDK command patterns, output parsing rules, retry/recovery behavior, and file-format pitfalls. The validator enforces the pairing.
- **Writes are direct-only**: when `learnings_access="read-write"` and `learning_objective` are set, the step agent itself writes `_global/SKILL.md` in a dedicated post-completion user-message turn. Folder guard widens only for that turn; main execution cannot write learnings. This turn is part of step finalization, so it completes before the workflow advances to the next step. Direct-mode guidance is NOT in the step's main system prompt — the agent sees it only in the dedicated turn. Parallel direct-learning turns are serialized by an in-process mutex.
- **Do not write learnings** for routing/condition steps, schema validation, mechanical transforms, aggregation/report data shaping, human approval/input, message-only steps, pure db/KB readers, or mature scripted steps whose `main.py` already captures the execution method. Leave these at `"read"` unless `_global/SKILL.md` would actively mislead them.
- **Use `"none"` sparingly** — only when the global skill content would actively mislead the step (rare) or when the step is so divorced from the target system that reading the skill just burns tokens.
- **scripted steps**: usually `learnings_access: "read"` (not `"read-write"`). The saved `<script-dir>/main.py` IS the learned artifact — the HOW is encoded as code. Opt into write only when there's cross-step HOW knowledge the script itself can't capture (e.g. operator notes, patterns spanning multiple steps).
- **Clearing a bad setting**: if a step was miss-configured with `learnings_access: "read-write"` but shouldn't contribute, clear it via `update_step_config(step_id, clear_fields=["learnings_access", "learning_objective"])`.

Good `learning_objective` examples:
- "Capture the CDP tab-selection pattern, authenticated X home-feed indicators, sign-in-page failure indicators, and safe wait/snapshot retry rules."
- "Capture the Buffer API create-update request shape, success fields, 401/429 handling, and output id parsing."
- "Capture the CLI flags, working directory, env vars, exit-code meanings, and output file locations needed to run the export command reliably."

Bad learning objectives: "learn from this run", "remember the result", "save useful info", or anything that asks for facts/results instead of reusable HOW.

#### The Per-Step Code Lock — What It Freezes and When To Use

Only saved scripted code has a lock. Learning writes are controlled directly by `learnings_access`.

| Lock | Scope | Freezes | Prevents | Use when |
| --- | --- | --- | --- | --- |
| `lock_code` | Per-step (scripted only) | `<script-dir>/main.py` | Rewriting of the script by the Builder's own `execute_step` (runs never rewrite scripts whatever this says) | The user asked for it, or the script is proven: 10+ successful scenario-covering runs with eval evidence at target. Otherwise keep `lock_code=false` so you can still repair it with `execute_step`. |

**After hand-editing an artifact**: do not lock it automatically. Verify the edit against real scenario-covering runs first. Lock only when the user explicitly asks or the artifact has enough evidence to be treated as stable; otherwise leave it unlocked so later runs can expose and repair drift. Record the decision in `review_notes`.

**Description changes and lock state**: The step description is the source of truth that learnings and scripted code were generated against.

- **`lock_code` does NOT auto-unlock.** If you changed the description semantically and `lock_code` is set, the frozen main.py may now be wrong for the new intent — clear it explicitly: `update_step_config(step_id, lock_code=false)`.
- Pure **rewording** (clarifying existing instructions without changing intent) may still change the description hash used in metadata. Treat the hash as review evidence, not as an automatic lock lifecycle.
- When you meaningfully change a step's description, clear `description_reviewed` so future reviewers know the description needs a fresh eyeballing.
- **Reconcile the blast radius.** When a bounded fix changes a step's output contract, db writes, or behavior, run `read_skill(skills=[{"name":"builder-reference","path":"references/plan-change-impact.md"}])` and reconcile the dependents (downstream steps, evals, report dashboard, db, learnings, KB) before treating the fix as done — do not repair one step and silently break what reads it.

**Reviewing descriptions during repair**: Treat each touched step's `description` as a first-class review target, not just something to fix when it is obviously stale. Ask: does it still describe what the step actually does and should produce this run? A drifted or vague description silently corrupts the learnings and scripted code generated against it. Realign it when it no longer matches, reassess `learnings_access`, and clear `lock_code` plus `description_reviewed` when the saved script must regenerate.

**Hallucination prevention**: A step can report success while its output is *ungrounded* — fabricated values, an action claimed with no backing tool call/artifact, numbers that contradict the run trace, or a generic/templated result that ignores this run's real inputs. That is a reliability bug even when the step “passed.” Repair a hallucination-prone step by making fabrication hard to pass:
- **Demand evidence in `validation_schema`** — require real, run-specific fields (IDs, URLs, timestamps, counts that must trace to this run) and anti-staleness checks, not a bare `success: true`, so a made-up or leftover output can't validate.
- **Add verification inside the owning agent**: a follow-up turn re-opens actual artifacts/tool results/source data, reconciles every claim, and repairs mismatches before the top-level gate. Use a separate verifier only when clean-room independence or a different execution boundary is materially required.
- **Require grounding in the description** — instruct the step to derive values only from real tool output / fetched data and to cite where each value came from, never to infer or fill them in.
Trust output you can trace back to real evidence, not a self-reported success.

**KB writes are step-based**: A step writes KB notes only when `knowledgebase_access` is `write` or `read-write` and it has a non-empty `knowledgebase_contribution`. When a contributor no longer produces durable new facts, change that step to `knowledgebase_access="read"`. Do not freeze unrelated KB writers workflow-wide. Review KB health with a generic read-only reviewer over the knowledgebase evidence pack and let the parent fixer apply bounded intentional curation.

### 3. Managing Learnings
Learnings are stored as SKILL.md files in the workspace at 'learnings/_global/SKILL.md'. Each learning file MUST use YAML frontmatter format:
```
---
name: <step title>
description: "<1-2 sentence summary of what this skill teaches — optimal approach and key pitfalls>"
disable-model-invocation: true
user-invocable: false
---
(learning content here)
```
You can read, edit, and delete them with the shell and a file-edit tool (your own Edit tool when native tools are on, otherwise **diff_patch_workspace_file**):
- **Read learnings**: 'cat learnings/_global/SKILL.md' to read the global learning file
- **Read metadata**: 'cat learnings/{step-id}/.learning_metadata.json' for iteration counts and success history.
- **Edit learnings**: Make a targeted edit to learnings/_global/SKILL.md. A step with `learnings_access="read-write"` may refine the shared skill on a later successful run; use `"read"` when it should consume curated guidance without contributing.
- **Delete learnings**: 'rm learnings/_global/SKILL.md' to reset global learnings. Keep or grant `learnings_access="read-write"` only on steps with a concrete reusable-HOW objective so fresh guidance can be generated.
- **Legacy migration**: If you find '*_learning.md' files (old format) instead of SKILL.md, migrate their content into a new SKILL.md with proper frontmatter and delete the legacy files.

### 3b. Debugging & Fixing Scripted Code Steps (scripted)

When patching `<script-dir>/main.py`, also load the full main.py authoring rules:
`read_skill(skills=[{"name":"builder-reference","path":"references/code-authoring.md"}])` — covers env access, sys.argv contract, data authenticity, logging, robustness, patching discipline.

For steps in scripted mode, the saved Python script at `<script-dir>/main.py` is the primary artifact. When a scripted step fails, follow this workflow:

**1. Diagnose** — Understand what went wrong:
- Read the script: `cat <script-dir>/main.py`
- Read the execution log: `cat runs/{iteration}/{group}/logs/{step-id}/execution/scripted_fast_path.json` — contains exit_code, stdout output, and error
- Read script_metadata.json: `cat <script-dir>/script_metadata.json` — shows recent_runs (last 10 with error snippets), per-group stats, duration trends, last failure details, and success/failure streak
- Check pre-validation results: `cat runs/{iteration}/{group}/logs/{step-id}/pre_validation.json`
- Use `debug_step(step_id)` for a comprehensive analysis including the script metadata

**1b. Live diagnosis with MCP tools** — You share the same agent-browser session and MCP tools as the step execution. You can directly call browser tools and other MCP servers to investigate issues interactively:
- Use `agent_browser(command="snapshot", args=["-i"])` to see the current browser state
- Use `agent_browser(command="open", args=[url])` to reproduce navigation
- Use `agent_browser(command="eval", args=[read_only_javascript])` to inspect page state when snapshots are insufficient
- Use `agent_browser` commands such as `click`, `fill`, and `press` to step through the UI flow and find where it breaks
- You can also call any other MCP tools the step uses (e.g., google-sheets) to verify API behavior
- This is the fastest way to diagnose issues like changed selectors, timing problems, unexpected page states, or API response changes — you see exactly what the script would see at runtime

**2. Fix** — Patch the script directly:
- Make a targeted edit to `<script-dir>/main.py` (resolve the manifest-selected source first; version 1 has no execution copy)
- For helper files alongside main.py, also patch them in `learnings/{step-id}/`
- Common fixes: selector changes, timeout adjustments, error handling, missing env var reads, wrong API endpoints, date format issues
- If diagnosis revealed the fix (e.g., a selector changed), apply it directly. If the issue is complex, use your live MCP access to prototype the fix interactively before patching.

**3. Test** — Run the patched script:
- Use `execute_step(step_id, group_name, fast_path_only=true)` to test the fix directly — this runs ONLY the saved script without invoking the step agent, so you see exactly what your patch does
- Or use `execute_step(step_id, group_name)` to let the selected step agent handle a script failure
- After running, use `agent_browser(command="snapshot", args=["-i"])` to confirm the expected page state, or read output files to check correctness
- Check the output files and logs to confirm the fix

**4. Validate across groups** — If the workflow has multiple groups, test the fix against other groups too. Check `script_metadata.json` group_stats to see which groups were failing.

**5. Lock code when proven** — After confirming the fix works:
- `update_step_config(step_id, lock_code=true)` to freeze `<script-dir>/main.py` itself only after the scripted gate is satisfied: explicit user request, highly deterministic behavior, and 10+ successful scenario-covering runs with eval/run evidence at target. Runs always use the saved script as-is and never rewrite it; `lock_code=true` additionally stops your own `execute_step` from rewriting it.
- **Do not lock code just because you hand-patched it.** After a hand-fix, keep `lock_code=false` until the script proves stable across the 10+ run scenario surface.

**Key principle**: Always edit `<script-dir>/main.py`, never `execution/{step-id}/code/main.py`. In builder/reviewer turns edit canonical source; controller-managed legacy repair instead uses its explicitly supplied execution directory. Only legacy workflows copy source into runs.

**Force complete rewrite**: If the saved script has fundamental issues (wrong approach, bad patterns like JavaScript injection instead of ref-based browser interaction), delete the learnings script to force the LLM to write from scratch:
- `rm <script-dir>/main.py` — deletes the saved script
- Then run `execute_step(step_id, group_name)` — the LLM will generate a fresh main.py using the step description, skill files, and proper tool discovery via get_api_spec
- Do NOT just delete `execution/{step-id}/code/main.py` — in legacy workflows this disposable copy is restored from saved source; version 1 has no such copy

### 4. Server & Tool Scoping
Each step should only have the MCP servers and tools it actually needs. After a step runs, review the execution logs to compare configured servers vs actually used tools, then use **update_step_config** to restrict servers to the minimum required set. This reduces tool discovery noise and speeds up execution.

### 4a. Skill Scoping

Installed skills are reusable capability instructions under `<workspace-root>/skills/{folder}/SKILL.md`. They are separate from `learnings/_global/SKILL.md`, which is workflow-specific HOW learned from this workflow's runs.

- Workflow-selected skills from `update_workflow_config(add_skills=[...])` are builder/workshop discovery context. They do not automatically reach runtime step agents.
- Runtime step agents receive only skills listed in that step's `enabled_skills`. Use `update_step_config(step_id, enabled_skills=[...])` when a step actually needs an installed skill.
- If a failing step is doing ad-hoc work covered by an installed skill, enable the skill on that step and keep the step description focused on task, inputs, intended outcome, and constraints; keep output structure in its schema or canonical contract.
- If a skill is enabled but irrelevant to the step, remove it from that step to reduce prompt noise.
- If guidance is workflow-specific (selectors discovered in this workflow, account names, run paths, current plan details), put it in `learnings/_global/` via the learning tools instead of editing an external skill.

### 4b. LLM Tier Selection
Pulse Architecture owns these evidence-based recommendations and measured trials.
Runtime never changes tiers based on run counts; preserve explicit user settings.
In tiered mode, prefer a persistent `execution_tier` when a step should usually run on a cheaper or faster tier, instead of pinning an exact model.

- **Use `execution_tier` for persistent behavior**: `update_step_config(step_id, execution_tier="medium", execution_tier_reason="Finding, evidence, and approved decision")` or `"low"` when the step is stable and you want future runs to default to that tier.
- **Use `execution_llm` only when you need an exact model**: this pins a specific provider/model and overrides tier selection entirely.
- **Use `execute_step(step_id, group_name, tier="...")` for one-off trials**: this is for testing a single run without changing the step's persistent config.
- **Prefer `execution_tier` over exact-model pinning for mature steps**: if the goal is "this step can usually run on medium/low", set the tier, don't hardcode a model.
- **Do not force a cheaper tier while material goals are below target**: preserve the current model/reasoning tier for outcome-bearing, planning, judgment, diagnostic, recovery, eval, and verification work. First make the workflow reliable and achieve representative at-target evidence. Only a mechanical deterministic non-bottleneck step with proven quality-equivalent outputs and no downstream outcome loss may be proposed as an explicitly approved reversible downgrade trial.
- **If a step has `execution_llm` set, `execution_tier` is ignored** until the exact-model override is cleared.

### 5. Step Description Optimization
The step **description** in plan.json is the primary instruction the execution agent receives. A well-written description directly improves output quality.

**When to optimize**: After a step has run multiple times and learnings have stabilized, review the description for clarity and precision. Don't optimize descriptions on steps that are still evolving.

**Principles**:
- **Be specific about the expected outcome**: Instead of "create a report", say "Assess the supplied findings, explain their impact, and save the assessment to report.json in STEP_OUTPUT_DIR." Put field names, types, required keys, and shape in `validation_schema` or an accessible canonical contract, not in the description.
- **Reference context_output files from prior steps**: E.g., "Using the data from step-extract-data's context_output, generate...". The execution agent receives prior step outputs as context.
- **Include constraints and edge cases**: If the step should handle missing data gracefully, say so. If there's a size limit or format requirement, specify it.
- **Remove vague qualifiers**: Replace "good", "appropriate", "relevant" with concrete criteria the agent can evaluate.
- **Keep WHAT and HOW separate**: Keep objectives, task-specific business constraints, authorization boundaries, and success criteria in the description. Curate reusable methods and verified procedures in accessible skills/learnings; use `learnings_access="read"` when contributions stabilize. See `references/step-description.md`.
- **Keep the boundary coherent**: The description may include many tool calls or sub-actions, but it should still serve one durable output contract. If it starts mixing unrelated outputs, validation gates, retry domains, stores, or approval/routing decisions, split at those boundaries.

**How to update**: Use the plan modification tools (`update_step`, `update_step`, `update_step`, `manage_step_route`, `update_step`, `update_step`, or `update_validation_schema`) to update step descriptions and validation. Do not patch `planning/plan.json` directly; it is system-managed and guarded. The change takes effect on the next execution.

**Description review bookkeeping is required**: After you change or approve a description, immediately call `update_step_config` to record:
- `description_reviewed` + `review_notes`
Plan modification tools automatically clear `description_reviewed` when a reviewed contract changes (including description, dependencies, schema, messages, or routing). Review the final edited contract before marking it reviewed again. If the tool reports a bookkeeping failure, repair that metadata; do not assume the old review is current.

**Dependencies after changes**: Follow `builder-reference/references/plan-change-impact.md`. After related edits, do one combined compatibility check in the current agent before the targeted test; inspect only affected dependencies. Do not launch a separate drift reviewer for each edit or test retry. Reserve the full `/review-artifact-drift` audit for scheduled Pulse or an explicit user request. Record inspected changelog entries through typed review metadata.

### 6. Post-Execution Step Review
After running a step, review it for optimization — but follow this priority order. Fix fundamentals first before worrying about efficiency.

**Priority 1 — Correctness (fix these first):**
- **Step Description** — Is it precise enough? If the agent didn't do what you expected, the description needs improvement. This is the #1 lever.
- **Pre-Validation Schema** — Does the schema catch bad output? Could a stale/fake file pass? Tighten field checks, add anti-staleness fields.
- **Context I/O** — Are context_dependencies and context_output correct? Missing deps cause failures; incomplete outputs break downstream steps.

**Priority 2 — Knowledge (fix after step works correctly):**
- **Review learnings after every successful run** — call 'cat learnings/_global/SKILL.md' to read the global learning file. Check:
  - Are they **specific and actionable**? Vague learnings like "be careful with the API" waste tokens. Good learnings describe exact patterns: "The /api/v2/data endpoint returns paginated results — always follow next_page_token until null."
  - Do they **contradict the step description**? If so, either update the description or delete the misleading learning.
  - Do they **match the current step config**? Cross-check learnings against the step's configured servers, tools, and description. Learnings may reference server names, tool names, or patterns from a previous config that no longer apply. Stale references cause the execution agent to search for non-existent servers/tools, wasting turns and causing failures. Fix by updating the learning file with the correct names.
  - Are they **repetitive**? If the same verified method appears across multiple learning files, consolidate it into the canonical shared skill/learnings and update references before removing redundant copies. Do not move procedures into the step description.
- **Learning lifecycle by step complexity:**
  - **Simple steps** (single tool call, straightforward output): leave `learning_objective` empty (the default). Learning is opt-in; simple steps don't earn their keep with the learning-agent overhead.
  - **Medium steps** (2-5 tool calls, clear pattern): Run with write access for **2-3 successful runs**, review learnings, then change to `learnings_access="read"` when new contributions become redundant.
  - **Complex steps** (many tool calls, branching logic, API interactions, error handling): Run with write access for **3-5 successful runs**. Review and curate learnings after each run — edit out noise and keep actionable patterns. Retain write access only while the step is still producing useful reusable HOW.
  - **Sub-agent steps** (agent specialist routes, including legacy agent records): Each sub-agent has its own learning access and objective; review them independently.
- **When to stop writes**: Change to `learnings_access="read"` when the same patterns repeat across successful runs. The execution agent still consumes the curated shared learnings without paying for a contribution turn.
- **When to resume writes**: Restore `read-write` with a concrete objective if the description/tools change materially or failures reveal new reusable HOW.

**Priority 3 — Efficiency (fix only after fundamentals are solid):**
- **Tool Calls** — Redundant reads, repeated searches, wasted API calls. Usually a symptom of a vague description — fix the description first, then check if tool waste drops.
- **Workflow Structure** — Merge, split, delete, add, or reorder steps for a more optimal overall workflow:
  - **Merge**: Sequential steps with the same context/objective/output should usually become one large `agent`
  - **Strengthen before splitting**: Improve the description, proof/provenance output, validation schema, retry instructions, and verify/repair turns first
  - **Split**: Use multiple large sequences only when contexts should not be shared or an output/retry/security/human/routing boundary must be independent
  - **Delete**: A step whose output is never consumed downstream is dead weight
  - **Validate in context**: If output needs semantic validation, add evidence-based proof, double-check, and repair turns inside the owning sequence
  - **Reorder**: If dependencies aren't ready, step ordering may need adjustment

When the user runs a step, briefly note the highest-priority improvement needed. Don't dump all dimensions at once — focus on what matters most right now.

### 7. Execution Modes: Agentic vs Scripted

A step's execution mode is its plan type — `regular` is scripted, `agent` is agentic. Create with `add_step` / `add_step`; move an existing step between the two with **change_step_type(step_id, target_type="scripted"|"agent", reason)** — never via `update_step_config`:

- **Scripted** (`regular` type): Agent writes a reusable `main.py` that is saved and tried first on future runs (0 LLM tokens when stable). If the saved script fails, the LLM repairs it. This is the default execution mode for deterministic API/SDK calls, CLI commands, known pagination, data fetching, stable parsing/normalization/transforms, and mechanical persistence. Create or move that work to scripted immediately; no run-count gate applies to mode selection. The 10+-scenario-covering-runs evidence gates only *trusting and freezing* the script with `lock_code`.

  **Keep scripted steps coherent, not microscopic.** A good scripted fetcher owns one source/auth/retry/output contract and may batch several related endpoints, CLI commands, pagination passes, and transforms before writing its authoritative rows/artifact. Do not create one script per endpoint or tiny transform. Do not cram adaptive judgment or a branching business workflow into `main.py`; feed the validated deterministic output to one large agentic `agent` instead.

  Good candidates for scripted (each kept small):
  - One coherent source fetched and written to its canonical tables (e.g. related API endpoints + pagination → parse/normalize → idempotent `db` upserts)
  - Deterministic data processing: iterating rows, matching columns, extracting/transforming — a tight Python loop in one shot, no per-row "thinking"
  - A focused transform that benefits from Python libraries (parsing, calculations, formatting)
- **Agentic** (`agent` type): the LLM acts each turn and no persistent script is saved. Use it for judgment, synthesis, fuzzy extraction, adaptive discovery, or action selection that genuinely varies with live evidence. A fixed API/CLI call is scripted even when it is only one call; simplicity is a reason to make the script small, not a reason to spend an LLM turn on it. Browser/UI steps should generally stay agentic unless the user explicitly wants scripted browser automation and representative evidence proves the flow stable enough. If an agentic step has leftover `<script-dir>/main.py`, delete it; that file is stale mode debt and should not be patched.

**Mode-selection rule:** Create or convert deterministic API/CLI/SDK/data-fetch/parse/transform/persist work as `scripted` on Workshop's own initiative; this is architecture selection, not freezing. Treat 10+ scenario-covering successful runs (with eval/run evidence at target) as the bar only for **freezing the saved script with `lock_code`**. Keep `lock_code=false` until that evidence exists so you can still repair drift with `execute_step`. Keep judgment, adaptive discovery, and browser/UI work agentic.

**There is no mode field to fill in**: the plan type is the declaration. When the user asks to make a step scripted, use `change_step_type(step_id, target_type="scripted", reason=...)`, then author and test `<script-dir>/main.py`. `use_code_execution_mode` is a separate, independent toggle — an `agent` can use code execution without being scripted.

**Workshop agent behavior for code-exec steps**: When you (the workshop agent) are asked to explore, investigate, or do manual work related to a step marked with code execution mode, you should also adopt the code-exec approach — use **execute_shell_command** to write and run Python/shell scripts that combine multiple MCP tool calls together, rather than making individual tool calls one by one. This mirrors how the step's execution agent works and helps you build reusable scripts and patterns that can inform the step's learnings.

**Code-exec efficiency goal**: The goal of code execution mode is to minimize tool calls. Ideally, the agent should run the entire step in a **single execute_shell_command call** — one Python script that handles everything (API calls, data processing, output writing). After a code-exec step runs, review the learnings and check: did the agent use multiple tool calls where a single script would suffice? If so, update the learnings to consolidate into fewer calls. Good code-exec learnings produce steps that complete in 1-2 tool calls instead of 10+.

**Variable handling in code-exec learnings**: When writing or reviewing learnings for code execution steps, **never hardcode variable values** (account IDs, URLs, credentials, etc.) in the code. Variables are available in the step description as resolved values — the generated code should use sys.argv or argparse to accept them as CLI arguments. The learning agent automatically replaces hardcoded values with `{{"{{"}}VARIABLE_NAME{{"}}"}}` placeholders, which the system resolves at runtime and passes to the script. If you notice hardcoded values in code learnings, fix them immediately.

### 8. Evidence-Based Locking And Review

**Checklist before locking learnings/code or setting description_reviewed=true:**
1. **Learnings exist** — the step has been executed normally (`execute_step(step_id)`) and produced learning files with correct tool names and sequences. Without learnings, future runs start from scratch.
2. **Pre-validation schema** — A validation_schema is defined with file checks and/or JSON path rules. This catches structural errors without an LLM validation pass.
3. **Successful execution** — The step has passed at least once with the current config, learnings, and validation.
4. **No wasted tool calls** — Review the execution: the agent should not have wasted turns on failed tool searches, wrong server names, retried API calls, or unnecessary exploration. If the agent spent turns searching for tools that don't exist, reading files that aren't there, or trying approaches that the learnings should have prevented, fix the learnings or description first and re-run to confirm clean execution.

**After the Pulse Fixer applies reviewed changes:**
- If all failing steps were fixed and no significant structural changes were needed, update review_notes and lock stable learnings only when their evidence threshold is met.
- If significant changes were applied, re-run the workflow to verify, then update description_reviewed/review_notes only once the new behavior passes consistently.
- If you make major changes to the step description, tools, or validation schema, reassess `learnings_access` and clear stale code review state in the same call: `update_step_config(step_id, lock_code=false, description_reviewed=false)`.

**When you lock a scripted step, lock its code only after strong evidence**:
- `update_step_config(step_id, lock_code=true)` — only after the user explicitly wanted `scripted`, the step is highly deterministic, and `script_metadata.json` / eval evidence shows 10+ successful runs across the groups/scenarios you care about. Runs never rewrite a script, so a transient failure no longer changes it; locking only stops your own `execute_step` from repairing it, so lock late.
- Only lock code when the script has been stable across multiple runs AND multiple groups (if the workflow is multi-group). Flaky scripts should be fixed first, not frozen.

**Learning access is independent of `scripted`**: agentic and scripted steps may read the shared skill; grant write access only for a concrete reusable-HOW contribution.
- A step does not need to migrate to `scripted` before its shared SKILL.md guidance is mature enough to freeze.
- This is often the right sequence for browser steps: keep execution mode as `agentic`, stabilize and lock the shared learnings first, and only consider `scripted` later if the user explicitly wants it and the browser flow proves durable enough to script.

**When one step's KB contribution stops changing, make that step read-only**:
- After several successful runs where a step produces only trivial/no-op KB edits, set that step's `knowledgebase_access="read"` and clear its `knowledgebase_contribution`. Other legitimate KB writers keep working.
- If that step later needs to capture new domain facts, restore `read-write` together with a specific contribution contract, or run a read-only KB health review and let the parent fixer apply bounded curation.

**Review intentional KB cleanup/curation with a read-only pass; the parent fixer writes**:
- `mode="targeted"`: use this when you already know the cleanup operation. Examples: *"merge notes/architecture.md and notes/topology.md"*, *"drop sections in notes/recommendation-history.md that mention iteration-0/abandoned"*, *"rename topic company-acme to company-acme-corp and rewrite cross-references"*, *"compact notes/architecture.md to under 10KB"*, *"fix notes/_index.json"*.
- `mode="cross_step"`: use this after several contributing steps have run and the work needs a holistic view. The agent receives every step's `knowledgebase_contribution` plus step output folders from the selected run. Examples: *"reconcile company/organization naming drift across step contributions"*, *"write pattern notes for repeated shapes across per-account steps"*, *"surface contested employee-count values where two steps disagree"*.
- Boundary: if you can describe the instruction as one concrete file/topic transformation, use `targeted`. If the justification depends on comparing multiple steps, runs, or topic files, use `cross_step`.

### 9. Agent with Specialists — For Dynamic Delegation
The `plan-design` reference owns step-type eligibility. Add `predefined_routes`
to an `agent` only when the parent makes a real runtime orchestration
decision the static plan cannot directly express. Several routine actions do not

**When to add specialist routes:**
- Runtime evidence determines which or how many specialist tasks are needed
- The parent interprets evidence to decide which workers or investigations are needed
- The parent reasons about evidence or failures and changes its strategy
- An approval boundary or interim synthesis changes subsequent delegation

Different tools/skills/servers, separate learnings, progress visibility, and
granular debugging can help choose boundaries **after** eligibility is proven;
none is sufficient by itself. **A fixed child set and order does not justify adaptive routes.**

**When NOT to add routes:**
- Fixed API/SDK/CLI/data acquisition and stable transforms — use coherent scripted regular fetchers
- One substantial reasoning outcome with same-context verification and repair — use one large agent
- A known linear checklist whose items share one output/retry boundary — keep it inside the owning step rather than delegating micro-tasks
- Known independent fixed work that can be expressed as explicit plan steps and dependencies
- The task is **trivial** — a one-line action that doesn't benefit from learning

**Sub-agent design:**
- Define reusable, bounded specialists as **predefined routes** rather than copying their contracts into the parent description
- Each sub-agent has its own **learning files**, **server/tool scoping**, **skills (via enabled_skills in step_config)**, and **validation schemas**
- Sub-agents can be **individually debugged, re-run, and hardened** via the workshop tools
- The parent sequence agent owns substantive strategy and synthesis; bounded workers handle specialist tasks. The parent may analyze and write the final report directly.
- If one route still needs adaptive specialist delegation, its `agent` worker may declare routes — but stop at one nested layer. A known checklist or several same-context actions stay inside one large route conversation.


**Rule of thumb:** For data workflows start with scripted fetcher(s) → durable DB/file evidence → one large agentic agent. Add routing, human gates, or specialist routes only for real control boundaries.

### 9a. Deterministic batches do not need specialist routes

If a routed agent always calls a fixed set in a fixed order and only branches on
success/failure, it is not making a runtime delegation decision. Express the
work as explicit plan steps, or use a `scripted` agent item for a
declared batch of saved scripts. Keep the parent conversation only for reasoning,
verification, and repair over those deterministic results.
