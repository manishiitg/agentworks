# AgentWorks self-healing workflow

Read the current `builder-reference` resources for `plan-design.md`, `step-description.md`, `stores.md`, `skill-management.md`, `playwright-scripted.md`, `browser-usage.md`, `routing.md`, `branch.md`, and `human-in-the-loop.md` as their operations become relevant. Resolve live tool schemas before mutations; preserve an existing plan and never edit `workflow.json` manually.

This workflow is a suggested pattern. Preserve the user's explicit process, tools, review policy, and compatible existing plan. Combine or split steps when the same authorization, isolation, verification, persistence, and reporting boundaries remain clear.

## Builder setup

Reuse the application profile, QA knowledgebase note, canonical suite/helpers, runner command, secrets, fixture policy, recording policy, and dashboard from the preceding playbooks. Configure permitted test/config paths, required related journeys, maximum diagnostic/repair attempts, reviewer, decision expiry, and optional patch/PR destination.

Declare repair tables and artifact ownership in `db/README.md` before writers. Author the dashboard once. Runtime steps write durable facts and evidence; they do not regenerate HTML. Configure KB consumers with read access and the post-acceptance updater with read-write access plus a precise contribution contract.

## Suggested saved-plan pattern

| Step ID | Type/tool | Responsibility | Next |
| --- | --- | --- | --- |
| `load-healing-candidate` | Scripted / `add_step(type="scripted", step={...})` | Resolve finding, original evidence, approved expectation, source/base revision and permitted paths; initialize repair; write `eligible` or `not_eligible`. | `eligibility-gate` |
| `eligibility-gate` | Branch / `add_step(type="branch", step={...})` | Deterministically select proposal or classification-only path. | `propose-test-repair` or `record-non-healable` |
| `record-non-healable` | Agent / `add_step(type="agent", step={...})` | Verify product/environment/unknown classification and evidence; persist disposition without changing tests. | `finalize-healing` |
| `propose-test-repair` | Agent / `add_step(type="agent", step={...})` | Inspect source/evidence and, when useful, perform a bounded recorded reproduction; write a candidate patch and rationale without touching canonical source. | `verify-candidate` |
| `verify-candidate` | Scripted / `add_step(type="scripted", step={...})` | Validate paths/base, apply patch in isolation, run static and journey checks, finalize/redact video and console/network evidence, and write `verified` or `not_verified`. | `verification-gate` |
| `verification-gate` | Branch / `add_step(type="branch", step={...})` | Send verified candidates to durable review persistence; send failures to finalization. | `queue-repair-review` or `finalize-healing` |
| `queue-repair-review` | Scripted / `add_step(type="scripted", step={...})` | Persist the exact candidate identity, patch/base hashes, verification and evidence as `pending_review`; call `create_human_input_request` with a stable repair-scoped ID, approve/reject/defer options, evidence, and bounded apply contract, then finish the preparation run. | `finalize-healing` |
| `load-approved-repair` | Scripted / `add_step(type="scripted", step={...})` | Start of the later apply route: load a stable repair/decision ID and verify approved status, reviewer authority, expiry, patch identity, and current base. | `approval-gate` |
| `approval-gate` | Branch / `add_step(type="branch", step={...})` | Deterministically send a valid current approval to application; send missing, rejected, deferred, expired, or stale decisions to finalization. | `apply-approved-repair` or `finalize-healing` |
| `apply-approved-repair` | Scripted / `add_step(type="scripted", step={...})` | Recheck the approval receipt, apply the exact patch once, optionally publish through an already configured destination, rerun canonical tests, and persist new source revision and final verification. | `update-healing-knowledge` |
| `update-healing-knowledge` | Agent / `add_step(type="agent", step={...})` | Verify canonical evidence and update the application KB only for an accepted, successfully rerun repair. | `finalize-healing` |
| `finalize-healing` | Scripted / `add_step(type="scripted", step={...})` | Check completeness, close repair lifecycle, calculate final state, and persist dashboard summary. | `end` |

The table is a design, not a payload to paste. Create target steps before branch references and wire every terminal path explicitly to `finalize-healing` or `end`; otherwise an unselected sibling may run by fallthrough. If the workflow already has one top-level mode router, add `self_heal_prepare` and `self_heal_apply` as separate sub-workflow routes selected by caller `route_selections`. Do not add a second routing step. The prepare route always ends after saving a pending review. The apply route starts only after a durable decision exists; eligibility, verification, and approval-validity decisions inside the modes are deterministic branches.

Branches contain no LLM judgment. A preceding producer writes `route_selection.json` with an exact `select_route`, and the branch declares the actual source/dependency. Missing or malformed selection must choose no passing path. Use `description`/`validation_schema` on producers; leave branch/router description and context output empty. A suite may contain several classifications, but this healing run should bind each candidate to a specific finding/repair ID rather than use one suite-wide bug category.

## Scripted steps

Inspect `workflow.json.code_layout_version`; author canonical Python `main.py` through the current scripted-step tools and let it launch the preserved JS/TS or Python test runner. Setup installs dependencies once. Scripts receive actual injected inputs, perform deterministic source/hash/patch/runner/DB work, and emit small validated context pointers.

Keep the test runner's nonzero result in the durable observation while allowing the wrapper to finalize valid QA data. Unexpected harness/persistence errors remain execution failures. Validate real DB rows/assets and permitted patch scope, not “all tests passed.” Leave `lock_code` false until the platform's maturity threshold is met.

Apply candidates in a run-scoped isolated working copy. Canonical application must remain unchanged before approval. The preparation route records `pending_review`, creates or refreshes the nonblocking platform decision, and exits; it never blocks waiting for the reviewer. The user answers later in the Pulse/report decision panel. `load-approved-repair` must call `get_human_input_request`; it and `apply-approved-repair` accept only the durable decision ID, approved repair/version, expected base hash, and verified patch hash. On a mismatch they record stale/needs-review rather than partially applying. After a terminal action, call `mark_human_input_consumed` with the truthful outcome. If publishing a PR is configured, save its real receipt; queued or attempted is not published.

## Message sequences

Use one conversation for evidence-based proposal and one for post-application KB verification. Give each a do → inspect evidence → repair omissions structure and focused schema. Attach `agent-browser` explicitly when managed reproduction is allowed; workflow-level selected skills do not cascade to runtime. Put customer facts in KB, methods in workflow learnings/skills, and run facts in DB.

## Recording and diagnostics

Keep the original failing attempt's video, console logs, and network logs. For Playwright verification, use runner-owned capture and the existing AgentWorks fixture policy; Browser-panel replay is temporary. For managed headless reproduction, use the deployed `agent_browser` bundled capture status/start/stop lifecycle in one session, stop captures this workflow started, inspect returned validation/files, and visually confirm the footage matches the attempt. CDP uses the deployed separate recording/HAR guidance.

Do not run duplicate capture systems on the same context. Copy permitted media/log/capture files into attempt-scoped paths under `db/assets/browser-qa/<run-id>/repairs/<repair-id>/`; record original versus candidate versus canonical phase plus complete/partial/missing/failed and redaction status. A path alone is not verified evidence. Follow the shared [evidence capture contract](../../references/evidence-capture.md).

## Acceptance coverage

Exercise eligible stale locator, preserved application bug, ambiguous expectation, prohibited assertion change, source drift, candidate failure, human reject/defer, approved exact apply, canonical rerun failure, missing/partial video, missing console/network evidence, redaction failure, cancellation, and duplicate invocation. Confirm no unapproved canonical write, no infinite retry, no overwritten original failure, and correct convergence to finalization.
