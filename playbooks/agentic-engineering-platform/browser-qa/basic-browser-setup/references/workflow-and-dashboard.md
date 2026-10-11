# Basic Browser Setup: steps and dashboard

This is a reference pattern. Preserve the user's preferred process and existing workflow. Combine, split, rename, or omit steps when the same setup, evidence, persistence, and safety outcomes remain verifiable.

## Separate builder setup from recurring execution

The builder performs discovery, writes verified helpers/tests, configures credentials/variables, declares durable schemas, and authors reporting once. Use the current tools and source layout; do not repeat dependency installation, test generation, or dashboard authoring on every smoke run.

The saved smoke workflow exercises those artifacts and records current readiness. Create a plan only when absent; preserve existing steps when extending a workflow. Resolve all tool schemas before mutations.

## Suggested saved-plan pattern

These are suggested stable step IDs and concrete responsibilities, not an importable plan JSON. Adapt IDs to the existing workflow.

| Step | AgentWorks type/tool | Inputs and outputs | Next step |
| --- | --- | --- | --- |
| `run-smoke` | Scripted via `add_step(type="scripted", step={...})` | Read profile/test refs and selected secret inputs; initialize a setup observation, execute the saved smoke spec, finalize/redact video and console/network evidence, and persist result/artifact status plus `route_selection.json`. | `smoke-outcome` |
| `smoke-outcome` | `branch` via `add_step(type="branch", step={...})` | Consume the preceding selection file: `verified` or `attention`. No LLM or output of its own. | `verified` → `verify-foundation`; `attention` → `diagnose-setup` |
| `verify-foundation` | `agent` via `add_step(type="agent", step={...})` | Read actual results/code/evidence, verify claimed locator/smoke/recording outcomes, then update the KB and profile status. Its follow-up item repairs missing documentation/proof without changing assertions. | Explicit `next_step_id: "end"` |
| `diagnose-setup` | `agent` via `add_step(type="agent", step={...})` | Inspect failures or missing evidence; persist blocked/needs-review status and an actionable explanation. Reuse authorized managed-browser investigation and recording when useful. | Explicit `next_step_id: "end"` |

The script's `verified` decision requires the saved smoke assertion to pass, required evidence to exist, and expected setup outputs to be present. Otherwise use `attention`; do not silently accept empty results. A QA failure is a recorded observation, not permission to hide the runner's nonzero exit. Preserve the original exit/error in data. A failure to persist a trustworthy observation is a step error; any unfinished observation stays incomplete.

For scripted code, inspect `workflow.json.code_layout_version`: use the canonical `code/<step-id>/main.py` for version 1 or the documented legacy layout. Author through `update_step(step_id=..., changes={"code": ..., "reason": ...})` and the current builder's code tools; preserve the platform's Python entrypoint even if it launches a JS/TS test suite. Put dependency installation in setup, use actual injected execution inputs, and keep `lock_code` off until the platform's evidence-based freezing criteria are met.

Configure a small `validation_schema` around observation identity, result/evidence references, and expected route values. Test failures must be valid structured observations; do not validate this step by demanding all application assertions pass. Declare its forward `context_output`, and configure the branch's `context_dependencies`/`route_source_file` from that actual output. Consumers of profile/observation data require their own explicit access and dependencies.

## Routing and branches

With only smoke verification, no mode router is needed. If setup verification and regression share one application workflow, use its single top-level `routing` step to select `verify_setup` versus `regression`, each pointing to a real first step of its sub-workflow. Reuse an existing router where present. A plan supports at most one newly authored `routing` mode selector; it is not the mechanism for every condition.

Use `branch` for readiness, skip/continue, and fixed approval decisions. A route/branch consumes an explicit selection such as `{"select_route":"attention"}`; it does not reason over test output. Its `routes` entries have stable `route_id`, `route_name`, `condition`, and existing `next_step_id`. Leave deterministic switch `description` and `context_output` empty. Create targets before wiring them, and set explicit terminal jumps to avoid falling through into an unselected sibling.

The caller can select known modes through `route_selections`; don't ask again for a supplied choice. Persist approvals that may outlive a run, finish preparation, and consume the later decision in a separate action route. Use `add_step(type="branch", step={"route_source": "human", ...})` only for an explicitly attended bounded choice; declare known free-form values as launch variables with `required_variables` on their first consumer, and use an agent's `human_feedback` for live interaction. Unattended runs must hold or defer rather than auto-approve. Read `builder-reference/references/routing.md`, `branch.md`, and `human-in-the-loop.md` for current mechanics.

## Dashboard authoring sequence

1. Read the existing report, `db/README.md`, and `builder-reference/references/reporting-policy.md`. Preserve unrelated report views.
2. Declare application/profile and setup-observation records, plus artifact references, with stable keys. Record source revision, application/environment, run/group identity, timestamp, status, smoke outcome, and blocker reason. Every expected video, console log, network log, screenshot, or trace needs its owning observation/attempt, source, durable path or null, format, capture/validation/redaction status, and missing reason.
3. Author or extend the single `db/reports/index.html` document. Add an Overview and an expandable Setup details area; adapt existing navigation rather than manufacture another dashboard system.
4. Bind every displayed result to durable rows or files. Put data calls behind `window.report.ready(...)`, and use documented query/get/media methods. No report-generation execution step or legacy JSON widget plan.
5. Validate with `validate_report_html()`, then open the actual Report view and verify saved data, controls, and playable media. Static validation cannot prove the UI renders or a dynamic path exists.

Suggested initial layout:

| Area | What it shows | Data source |
| --- | --- | --- |
| Header | Application, environment, selected observation/time | Profile and setup observation |
| Readiness | Browser, auth/fixture, runner, locator, smoke, and recording states | Actual setup observations; unknown stays unknown |
| Smoke result | Expected/observed behavior, outcome, source revision | Assertion result and tested source refs |
| Evidence | Video playback; safe Console and Network views; screenshots and trace/capture downloads | Durable attempt-scoped artifact records |
| Next action | Setup blocker or entry point for critical-journey validation | Persisted finding and real configured workflow IDs |

Store report-visible files under `db/assets/`; reports cannot directly open test repository paths or `knowledgebase/` paths. Show such source locations as text or an authorized DB snapshot summary, not broken report links. Keep the canonical KB note outside the report unchanged.

Load a recording only when opened, using a fresh `window.report.mediaUrl(path)` and a native video player with `controls` and `preload="metadata"`. Do not persist expiring media URLs or reset a playing video on every data refresh. Render redacted console/network records as escaped text with useful filters; never render captured response HTML. Show absent/partial/failed artifacts distinctly. Follow [the evidence capture contract](../../references/evidence-capture.md).

A Run smoke or Continue setup button may enqueue a specific request through the documented `window.report.sendChatMessage` from a user click only. Resolve actual workflow/step IDs and permissions. Show queued separately from running/completed; no execution or notification from render/refresh callbacks. Omit controls that cannot be wired to supported actions.

Check empty, configured-but-unverified, blocked, passed, missing-video, missing-console, missing-network, partial-capture, redaction-failed, and failed-data-load states. Verify the selected observation's evidence is never replaced with artifacts from another attempt.
