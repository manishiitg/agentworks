# AgentWorks plan and tool guide for Browser QA

Use this reference while creating or revising a Browser QA workflow. Start from the user's requested process and the existing plan. Treat the patterns below as defaults to adapt, not a required graph. Resolve the live tool schemas and current `builder-reference` before making changes.

## Choose useful step boundaries

A workflow step is a durable execution boundary, not a browser action. Keep clicking, filling, screenshots, assertions, and routine retries inside the step that owns the outcome.

Split work when at least one of these changes:

- a durable output or downstream contract;
- an independently retryable failure domain;
- tools, credentials, permissions, or runtime;
- a human decision or a fixed route choice;
- deterministic execution versus agentic judgment;
- the persistent store being written; or
- the need for an independently validated result.

Otherwise prefer one coherent step with a clear output and validation contract.

## Select the step type

| Need | Step type | AgentWorks planning tool | Browser QA examples |
| --- | --- | --- | --- |
| Reasoning, investigation, or adaptation with shared context | `agent` | `add_step(type="agent", step={...})` | Explore an unknown application, diagnose a failure, compare evidence, propose an eligible repair. Use substantial do, verify, and repair items rather than one item per action. |
| Fixed execution with deterministic inputs and outputs | `scripted` | `add_step(type="scripted", step={...})`, then `update_step(step_id=..., changes={"code": ..., "reason": ...})` | Run a saved Playwright suite, perform readiness checks, normalize results, apply an approved patch, or persist mechanical output. The checked-in `main.py` may invoke the existing JS, TS, or Python test runner. |
| Small in-flow choice that quickly converges | `branch` | `add_step(type="branch", step={...})` | Ready versus blocked or product defect versus test defect. A deterministic or agentic producer should write the route-selection evidence first. Use `route_source="human"` only for an explicitly attended, bounded decision. Persist long-lived approvals and consume them through a later action route. |
| One major mode selector for substantially different sub-workflows | `routing` | `add_step(type="routing", step={...})` | Choose setup, smoke, regression, or self-healing mode when one workflow intentionally supports those large paths. Use at most one routing step. The caller, schedule, or a prior producer supplies `route_selections`; every route starts a real subflow. |
| A known free-form value from a person | Launch input, not a step | Declare variables and the first consumer's `required_variables` | Supply the base URL, test account label, or run note before launch. Live interaction uses an agent's `human_feedback`; long-lived approvals use durable requests and a later consumer. |
| The parent must decide and revise the investigation strategy at runtime | `agent` with `predefined_routes` | `add_step(type="agent", step={...})`; `manage_step_route` for route edits | A broad investigation where evidence determines which specialist checks to run next. Do not add routes for a fixed suite, known checklist, simple parallelism, or a fixed retry rule. |
| Work belongs to a persistent specialist | `crew` | `add_step(type="crew", step={...})` | Invoke an attached Crew's enabled trigger and wait for its response. Discover the Crew and trigger before authoring the step. |
| Reusable deterministic helper that should not run in the main path | orphan `scripted` step | `add_step(type="scripted", step={"is_orphan": true, ...})` | A selector audit, evidence exporter, or one-off repair verifier launched from Workshop or referenced by a supported scripted batch. |

Use `update_step(step_id, changes)` to revise a step and `change_step_type` for supported `scripted` ↔ `agent` or `routing` ↔ `branch` conversions. Use `delete_plan_steps` when removing steps. Read `builder-reference/references/plan-editing-tools.md` and the live selected schema before mutations; fields belong inside `step`, `changes` or `parameters`. External MCP clients delegate through `builder action=chat` only when it is listed. Never patch `planning/plan.json` directly.

## Recommended Browser QA composition

Use only the boundaries the customer process needs. A typical composition is:

1. **Discover or diagnose — `agent`.** Read the application profile and relevant knowledgebase notes, use the managed browser, and produce verified findings or a repair proposal.
2. **Execute — `scripted`.** Run the canonical Playwright command with bounded timeouts, the configured fixtures, and runner-owned recording. Emit machine-readable case and attempt results.
3. **Classify — keep it with its owner when possible.** Deterministic status derivation belongs in the runner or a small script. Judgment about whether the application or test is wrong belongs in an agent.
4. **Choose — `branch` only when paths differ.** Consume an explicit status or route-selection file. Do not add a branch when failure should simply stop the owning step.
5. **Queue review — persist and end.** Save the concrete diff, evidence, verification, hashes, and `pending_review` decision for later review. Do not keep the run open by default.
6. **Apply and rerun — separate `scripted` action route.** Load the durable approval, recheck proposal and base identity, apply only the approved test change, rerun the canonical affected tests, and fail closed on incomplete coverage.
7. **Persist and present.** Mechanical result writes belong in the scripted owner. Verified application facts can be contributed by the discovering agent. The report reads durable data rather than volatile run files.

Do not create separate steps for every route, click, locator, screenshot, assertion, report card, or retry.

## Configure the workflow with AgentWorks tools

### Plan and validation

- Use `add_step(type, step)` only after choosing one of the five supported types above. Supply required native fields, including `reason`, inside `step`; use the current schema rather than treating the examples as complete payloads.
- Use `update_validation_schema` for machine-checkable completion. Validate expected case IDs, terminal status for every required case, evidence references, source revisions, and completeness counts. A summary saying “passed” is insufficient.
- Use `context_output` and `context_dependencies` for compact within-run handoffs. A route producer should write the exact route-selection contract expected by its branch or routing step.
- Use `update_step_config` for runtime skills, custom tools, execution tier, KB access/contribution, learnings access/objective, and code locking. Keep `lock_code=false` while a scripted step is still being proven; lock only under the current platform criteria.
- Use `update_workflow_config` for workflow-level browser mode, selected skills, MCP servers/tools, variables, secrets, and notification instructions. Workflow-selected skills aid builder discovery; they do not automatically reach runtime steps.

### Browser exploration and evidence

- Use managed `agent_browser` for live exploration, locator discovery, console/network inspection, screenshots, traces, and browser capture. Load the current command guide through the platform skill system, check `agent_browser status`, and use the supported `open`, tab, snapshot, click, fill, press, diagnostic, and capture commands. Follow the shared evidence contract when retaining console logs, network logs, or video.
- Configure the browser step with `enabled_skills=["agent-browser"]` and the currently required custom browser tool through `update_step_config`. Set the supported `browser_mode` through `update_workflow_config`; never infer that CDP is available without checking status.
- Use `open_workspace_view(view="browser")` when the user should watch managed browsing or a Playwright session. Browser recording supports discovery evidence; Playwright recording should remain runner-owned for repeatable tests.
- Keep browser actions inside the managed browser tool. Do not run browser commands through a shell or bypass it with raw CDP.

### Saved Playwright execution

- Put repeatable smoke and regression paths in a `scripted` step and checked-in canonical test code. Follow the current `builder-reference` Playwright guidance and use the supported AgentWorks Playwright runtime when available; preserve an explicitly selected compatible runner.
- Let the wrapper return deterministic status, case coverage, timings, evidence paths, and source/config revisions. Use stable semantic locators and shared locator helpers. Do not treat conversational browser exploration as the canonical regression suite.
- Trial a scripted step with `execute_step(step_id=..., group_name=..., fast_path_only=true)`. Use normal `execute_step` when the step agent and configured repair behavior are part of the test.

### Persistence, knowledge, and reporting

| Information | Store and tool boundary |
| --- | --- |
| Small handoff used only later in the same run | `context_output` plus downstream `context_dependencies`; it is volatile and must not power the dashboard. |
| Run, case, attempt, repair, approval, and evidence metadata | Tables in `db/db.sqlite`, declared first in `db/README.md`. Agentic steps use `query_workflow_db` and `mutate_workflow_db`; scripted code uses the provided `$DB_PATH`. Schema changes use `apply_workflow_db_migration`. |
| Video, console logs, network logs/HAR, screenshots, traces, and other durable files | `db/assets/`, with one database artifact row containing attempt ownership, provenance, capture/validation/redaction status, and a reference. |
| User-supplied application rules or preferences | `knowledgebase/context/context.md`; grant required steps `knowledgebase_access="read"` and name the relevant section in their descriptions. |
| Verified application facts such as stable selectors, auth quirks, and route behavior | `knowledgebase/notes/` through a precise `knowledgebase_contribution` on the discovering step. Keep executable selectors in canonical test helpers and link the note to code/evidence. |
| Reusable execution technique | `learnings/_global/SKILL.md`, only from a step with `learnings_access="read-write"` and a narrow `learning_objective`. |
| Reusable cross-customer guidance | The playbook skill and its references; do not add customer-specific discoveries here. |

Build the dashboard at `db/reports/index.html` over durable tables and assets through `window.report`. After editing, call `validate_report_html()`, open it with `open_workspace_view(view="report", target=...)`, and verify the first render, empty state, failure state, filters, links, and recording playback.

### Run and debug

- Use `execute_step` for a targeted Workshop run and `run_full_workflow` for one complete selected group. Supply known values as declared launch `variables` or in the selected group, scoped instructions through `human_inputs`, and fixed route choices through `route_selections`. Set the first consumer's `required_variables` so missing values stop execution.
- Use `query_step`, `list_executions`, and `debug_step` for a bounded status or diagnostic check. Follow platform auto-notifications rather than building polling loops.
- Use `send_step_message` to correct an active step by its returned execution ID. Use `stop_step` or `stop_all_executions` when execution must be cancelled.
- Re-run the smallest affected deterministic step first, then the selected end-to-end route. Confirm persisted rows, durable evidence, and the live report as well as the step output.

## Tool-selection checklist

Before treating the workflow as ready:

1. Inspect the existing plan, skill selection, browser status, runner, DB contract, KB index, and report.
2. Preserve explicit customer choices and reuse compatible steps before adding new ones.
3. Choose the fewest durable step boundaries and attach only the tools and runtime skills each step needs.
4. Define validation, context handoffs, database upserts, asset provenance, and KB contribution before the first trial.
5. Run the saved path from a fresh context, verify every required case, and exercise a meaningful failure state.
6. Verify the dashboard from durable data and record remaining blockers without converting them to passes.
