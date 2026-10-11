# Regression workflow: steps, routes, and recording

Read current `builder-reference/references/plan-design.md`, `routing.md`, `branch.md`, `playwright-scripted.md`, and `browser-usage.md` before configuring the matching operations. This guide supplies a concrete initial plan, not a static API payload to paste without resolving tool schemas.

Treat the plan as a starting pattern. The builder preserves explicit user preferences and compatible existing structure, then chooses the smallest set of steps and routes that keeps retry, evidence, permission, and persistence boundaries clear.

## Builder work before the first run

Reuse the foundation's KB note, runner, account references, fixtures, locator helpers, and recording policy. Agree on the journey manifest and expected outcomes, add missing tests/helpers, and verify them on an authorized fixture. Pin profile and journey revisions; record tests and imported helper source hashes. Configure an explicit journey set for regression; selective/PR impact testing is an optional later feature, not an implicit substitute for the requested coverage.

The builder declares DB contracts and authors the dashboard once. Saved scripts execute tests and persist facts; investigation agents interpret those facts. A recorded bug does not cause the agent to rewrite product code or assertions during this initial validation workflow.

## Suggested saved-plan pattern

| Step ID | Type and creation tool | Responsibility and durable output | Next |
| --- | --- | --- | --- |
| `prepare-regression` | Scripted / `add_step(type="scripted", step={...})` | Initialize run + expected cases; resolve profile, test source, variables, fixtures, and dependencies. Record readiness and a selection file. | `readiness-gate` |
| `readiness-gate` | Branch / `add_step(type="branch", step={...})` | `ready` → execute suite; `blocked` → investigation. | `execute-regression` or `investigate-regression` |
| `execute-regression` | Scripted / `add_step(type="scripted", step={...})` | Run the saved manifest, persist original outcomes, finalize/redact attempt video and console/network logs, clean fixtures, and write `clean` or `attention`. | `investigation-gate` |
| `investigation-gate` | Branch / `add_step(type="branch", step={...})` | `clean` → aggregation; `attention` → investigation. | `finalize-regression` or `investigate-regression` |
| `investigate-regression` | Agent / `add_step(type="agent", step={...})` | Inspect all relevant findings, capture bounded reproductions if useful, record classifications and repair proposals, verify evidence, and update verified KB knowledge. | Explicit `next_step_id: "finalize-regression"` |
| `finalize-regression` | Scripted / `add_step(type="scripted", step={...})` | Check case-set completeness and evidence, apply QA status rules, close the run, and persist summary rows for the dashboard. No HTML generation. | Explicit `next_step_id: "end"` |

The branches protect real expensive-work and judgment boundaries. A small implementation may combine preflight into the runner if both share one failure domain; preserve the same output and completeness guarantees. Do not split tests into one agent per click or introduce an agent with predefined routes for a fixed case list. If independent suites need separate execution boundaries, keep explicit manifests/results; use only supported script batch mechanics with bounded concurrency.

Configure investigation to accept preflight-blocked runs that have no suite output. Give it the run pointer and query the DB; do not require a skipped runner's nonexistent file as a dependency. Every branch path must reach finalization or leave a durable explicit interrupted/error state when execution itself aborts.

## Scripted step contract

- Resolve code layout from the current workflow; retain the saved Python `main.py` entrypoint and invoke the existing JS/TS or Python runner from it as appropriate. Use `update_step(step_id=..., changes={"code": ..., "reason": ...})` and current builder tools. Do not install dependencies every run.
- Inject configuration and secrets through supported runtime inputs. Use the actual test working directory and runner command saved in the foundation. Keep fresh test contexts and independent fixture state.
- Persist run identity/expected cases before execution and write terminal case records in cleanup/finalization paths. Bounded per-case and run watchdogs must leave evidence; the outer process kill is not a normal retry strategy.
- Preserve the test runner's exit code and assertion failures in records. The wrapper may successfully process a failing QA result and return valid structured output. Unexpected harness errors, missing manifests, or failed persistence remain execution errors and must not produce a `clean` selection.
- Keep infrastructure retries bounded and separate; diagnostic reruns get new attempt IDs. Initial failures never disappear from history because a later attempt passes.
- Configure a light schema for run IDs, required output shape, real durable records, and completeness. Do not set a schema condition that merely demands zero failed application tests.
- Expose a small `context_output` containing run/result pointers; separated consumers declare `context_dependencies`. Durable DB rows and assets remain the report source.

## Exact route semantics

For standalone regression there is no need for a top-level router. If multiple major modes share the application workflow, use its **one** `routing` step, for example `verify_setup` → `run-smoke` and `regression` → `prepare-regression`. Each target must already exist and start a real sub-workflow; a router route cannot point straight to `end`.

Use branches for every smaller decision. The source step writes `route_selection.json` with `{"select_route":"ready"}` or the matching route ID. Configure `route_source_file` or an actual `context_dependencies` reference; when multiple producers use that filename, resolve the correct producer explicitly. Routes carry `route_id`, readable `route_name`, `condition`, and `next_step_id`. The branch/router itself has no agent, description, or context output. The required question is a readable label, not an LLM classifier.

For `readiness-gate`, `ready` means prerequisites were verified; otherwise persist `blocked`. For `investigation-gate`, `clean` requires a complete expected result set with no failure, ambiguity, cleanup concern, or evidence gap requiring judgment. All other valid observations select `attention`. A missing selection must not fall back to the passing path; use an explicit attention/blocked fallback or surface a workflow error. Malformed selection is an error.

Wire terminal steps with explicit `next_step_id` so execution cannot fall through into an unselected sibling. Create targets before references; remove/reroute incoming edges before deleting a target. Use caller `route_selections` for a known requested mode rather than ask the same question again.

Do not branch once between application bug / stale test / environment issue for the entire suite: one run can contain all three. Investigation processes every case and saves classifications individually. Persist case-specific review decisions and let a later authorized remediation route consume them. Use `branch` with `route_source="human"` only for an explicitly attended bounded choice; known free-form values must be declared launch variables with `required_variables` on their first consumer, and live interaction uses an agent's `human_feedback`. Unattended runs defer unresolved decisions rather than auto-approve.

## Browser recording and diagnostics

Reuse the foundation's explicit evidence/retention policy. A reasonable initial proposal is retaining failed-case video, console errors/warnings, and failed/navigation network activity; preserve the customer's existing policy. Retain the original failing attempt, not just the diagnostic reproduction. If any artifact is required evidence, its absence prevents complete verification; otherwise display the evidence warning without inventing an application failure.

For Playwright suites, keep runner-owned context/video lifecycle and finalize it before collecting paths. AgentWorks' optional Browser-panel stream/replay is separate from runner videos. Temporary replays can disappear and live streaming is skipped in unattended contexts; scheduled regression must use its runner's evidence policy. Avoid enabling duplicate capture systems on the same test context.

For an authorized managed headless reproduction, use the built-in recording service through `agent_browser(command="capture", args=["status"], session=...)`, then `start`, reproduce in that same session, and `stop`. Use the session and returned paths actually provided; capture assigns its own output directory. Stop only captures this workflow started, including on failure. Check status after timeouts and resolve partial stop errors. Do not mix bundled capture with parallel `record` or HAR start/stop commands. In CDP, use the deployed guide's separate video/HAR commands instead of assuming the bundle is supported.

Inspect returned validation and files, and visually verify that footage belongs to the current attempt before presenting it. A returned path alone is not proof of useful footage. Record complete, partial, missing, or failed capture explicitly; do not claim microphone audio or simultaneously recorded background tabs.

Copy permitted video, console, network/HAR, screenshot, trace, and capture artifacts into authorized `db/assets/browser-qa/<run-id>/...` locations. Persist owning run/case/attempt, phase, source, durable path, format, capture/validation/redaction status, and missing reason. Never retain credentials or unrestricted HAR bodies. Preserve authentication and existing browser state when stopping capture. Follow the shared [evidence capture contract](../../references/evidence-capture.md).

## Acceptance checks

Verify a passing suite, a real failed assertion, expired auth, an omitted case, timeout/cancellation, multiple failure categories, and missing/partial video, console, or network capture. Confirm the blocked path never launches tests, clean path skips investigation, attention path runs investigation once, and every selected path converges without executing an unrelated sibling. Missing rows or evidence must remain visible even when the runner exited successfully. See [dashboard setup](dashboard.md) for UI verification.
