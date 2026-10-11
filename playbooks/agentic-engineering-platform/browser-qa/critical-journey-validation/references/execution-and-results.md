# Execution, investigation, and results

## AgentWorks builder wiring

Read operation-specific resources from the attached `builder-reference` using `read_skill`: `references/plan-design.md`, `references/step-description.md`, `references/skill-management.md`, and `references/stores.md` for construction/persistence; `references/browser-usage.md` for managed investigation; `references/playwright-scripted.md` for repeatable execution; and `references/reporting-policy.md` for reporting.

Use current plan/config/schema tools rather than manual workflow JSON or legacy report widgets. Use `add_step(type="scripted", step={...})` for the saved suite and `add_step(type="agent", step={...})` for coherent investigation and evidence verification. Preserve runner exit status while allowing QA failures to reach persistence/reporting. Split only for meaningful retry, permission, human-decision, or durable-output boundaries. Use `context_output`/`context_dependencies` and a focused `validation_schema` for actual consumers.

Explicitly attach `agent-browser` to managed-browser steps with `update_step_config(enabled_skills=...)`; workflow-selected skills do not cascade to runtime. Scope other skills to the steps that need them. When a human decision is necessary, use the existing lifecycle described in `references/human-in-the-loop.md`. Use managed browser commands for investigation and the supported suite integration for repeatable tests, preserving the existing language and fixtures.

## Foundation input

Consume a versioned profile with application/environment identity, exact allowed origins, browser mode/tool choice, account-role references, test-data boundaries/reset method, reporting destination, and prior readiness evidence. `browser-foundation/v1` from Basic Browser Setup is the initial contract. Its `configured`, `ready`, `blocked`, or `needs_review` value describes setup, not the outcome of this run.

Resolve the canonical suite, runner invocation, config, smoke specs, shared locator helpers, and locator verification references. Missing or inaccessible sources require setup completion; a profile path alone does not prove tests exist. Preserve source revisions/hashes for tests and imported helpers in every run's provenance.

Use saved locator factories/page objects that resolve current elements by role/name, label, scoped test ID, or a verified semantic hook. Verify new controls' uniqueness and expected behavior before adding a shared helper. Never persist `@e1`-style snapshot refs, coordinates, or live element handles as executable selectors. Keep verification scope and last observed state/build alongside source references. Diagnose drift and propose a canonical helper repair without silently changing assertions. These choices follow [Playwright's locator guidance](https://playwright.dev/docs/locators).

## Knowledgebase reuse and updates

Read the profile's application-specific browser QA note before expanding coverage or diagnosing selector drift. It contains verified locator expressions/strategies, page/frame scope, role/state, canonical helper symbols, verification evidence/revision, and runner/auth/fixture setup. Resolve executable locators from test code; KB documentation is not an alternate runtime source. If the note is missing, reconstruct it from verified source/evidence rather than claim setup knowledge was persisted.

Configure consuming steps with `knowledgebase_access="read"` and name the note in their descriptions. Configure only verified-knowledge producers with `knowledgebase_access="read-write"` plus a precise `knowledgebase_contribution` naming the note and permitted updates. Record new verified locator knowledge, setup changes, and durable findings; retain links to evidence and source revisions. Mark uncertain or stale entries explicitly. Proposed repairs do not replace verified entries before acceptance and revalidation.

Keep chronological attempts/results in the DB and executable selectors in shared helpers. Keep the application note concise and source-linked; don't duplicate it into shared builder skills or accumulate all runtime logs there. Never include secret values. For separate workflows, wire supported KB access or transfer an authorized snapshot explicitly.

An equivalent existing setup is valid: document its mapping rather than force reinstallation. Reuse the same workflow when appropriate. When a new workflow is required, explicitly transfer an authorized non-secret snapshot and select credentials independently; no implicit cross-workflow file access. Pin the profile revision and journey revision used by each run. Unknown build identity stays null with an explanation; do not infer a deployed build from repository HEAD.

## Journey contract

Each journey has a stable ID/name, required flag, actor role, initial state, action sequence, observable assertions, expectation source, and fixture cleanup. Prefer business outcomes over clicking through pages: a save confirmation is weaker evidence than the changed record remaining correct after refresh.

Validate permissions through customer-approved role scenarios. Stay within approved accounts/origins and test-data boundaries. Do not add production mutations, purchases, invitations to real people, or security probing just because the browser supports them.

The example contains login, create, and edit scenarios on a fictional app. The create/edit cases explicitly start from independent state. Replace examples with approved behavior and actual fixture setup.

## Run and attempt contract

Declare the durable schema in `db/README.md` before writers or report queries are created. Adapt an existing store rather than add duplicate tables. A useful initial mapping is:

| Record | Required information |
| --- | --- |
| Suite run | Unique run ID, actual execution/group identity, application, environment, build identity or explicit unknown, source playbook/version, profile and journey revisions, start/end times, expected journey IDs, lifecycle, QA status. |
| Case result | Run ID + journey ID, required flag, status, assertion observations, reason, failure classification, attempt-scoped evidence references, cleanup outcome. |
| Attempt | Case key + attempt ID, original/diagnostic purpose, start/end times, observed result, runner exit/error when applicable, expected and observed video/console/network evidence. |

Use primary keys and idempotent upserts scoped to a specific run/case/attempt. Preserve separate runs and variable groups. Write a running suite and expected case list before browser work. Finalize cases on cancellation/error where possible. An interrupted run whose records cannot be finalized stays explicitly incomplete; it never becomes passed because no failure row exists.

Keep execution lifecycle (`running`, `completed`, `cancelled`, `interrupted`) separate from QA status (`passed`, `failed`, `blocked`, `needs_review`, `incomplete`). Case statuses may also include `skipped` and `not_run`. A schema-valid `failed` QA result can be successful workflow processing of a discovered bug.

### Suite completeness

Compare actual case IDs with the requested set before assigning QA status. The requested set must be nonempty and contain at least one required journey; otherwise request a meaningful acceptance set before execution. Reject duplicate/missing required results as incomplete rather than counting them as passes.

For a completed lifecycle:

1. A confirmed required assertion failure makes the suite `failed`; still report any missing coverage.
2. Otherwise a required readiness/environment blocker makes it `blocked`.
3. Otherwise unresolved required observations or expectations make it `needs_review`.
4. Otherwise missing/not-run/skipped required cases make it `incomplete`.
5. Only when every required journey executed and passed may it be `passed`. Optional failures and skipped coverage remain visibly qualified alongside that status.

Cancelled/interrupted/running lifecycle is always displayed prominently and cannot produce a passed suite. An unexplained failed assertion remains a failure or unresolved finding even if its root cause is unknown. Empty results are not evidence of correctness.

## Failure classification

| Classification | Evidence and response |
| --- | --- |
| Application bug | Approved expected behavior conflicts with observed outcome; retain reproduction and assertion evidence. Propose a bug report. |
| Stale test | Intended behavior still holds, but the locating/setup procedure no longer matches; propose a repair preserving the assertion. |
| Environment/setup | Unavailable origin, authentication prerequisite, fixture failure, or missing runtime dependency; report a blocker and its owner. |
| Intermittent failure | Bounded diagnostic attempts differ; preserve every attempt and identify evidence of timing/environment variance. A later pass does not clear the original finding. |
| Unknown | Available evidence cannot distinguish causes; state what is missing and route for review. |

Capture video, screenshots, observed URL/state, last completed action, assertion expected/actual values, and console/network/trace evidence according to policy. Record unavailable evidence explicitly rather than fabricate a link. Redact secrets and unnecessary customer data, with network response bodies disabled by default. Use stable artifact paths under `db/assets/browser-qa/<run-id>/`; `STEP_OUTPUT_DIR` and `runs/...` alone are not durable reporting locations. Follow the shared [evidence capture contract](../../references/evidence-capture.md).

Do not change expected values, delete assertions, or patch product code to make this validation pass. Test repairs and coding-agent fixes are proposed follow-up work in this initial playbook. If later authorized, retain the original failure and record verification of the exact accepted change separately.

## Reporting setup

Read current `builder-reference/references/reporting-policy.md`. Author or extend `db/reports/index.html`; preserve other views. Do not create a JSON widget plan or a recurring report-generation step.

Configure the report to query durable run/case/attempt/artifact data inside `window.report.ready(...)`. Show application/environment/build, run selector, lifecycle, QA status, required coverage counts, each journey's assertions/status, findings, video, console/network evidence, and unresolved work. Preserve historical run/attempt identity; do not mix current profile values or evidence from another attempt into an older result.

Use `window.report.fileUrl`/`openFile` for appropriate files and `mediaUrl` for audio/video as documented. Escape arbitrary page strings and render trusted structure separately. Missing files, no runs, running cases, failed queries, and interrupted runs need explicit states. Runtime steps persist data; reports read it live.

Run `validate_report_html()` and inspect it with saved passing, failing, and incomplete data. External summaries or issue creation are opt-in customer configuration. When authorized, use a stable run/case key for deduplication and persist delivery receipts; delivery failure does not change the underlying QA result. Route run-summary presentation through `update_workflow_config(run_notification_instructions=...)` when applicable. Do not enable a notification channel from a tool recommendation.

## Behavioral acceptance cases

| Fixture situation | Required observable outcome |
| --- | --- |
| All required journeys satisfy approved assertions | Passed result tied to the exact run and retained evidence. |
| Saved value is wrong after refresh | Failed assertion and bug evidence; expected value remains unchanged. |
| Login prerequisite fails | Blocked affected cases, not a set of invented product bugs. |
| Button moved; original business outcome still holds | Stale-test finding/repair proposal, preserving initial failure. |
| Initial failure followed by a passing diagnostic attempt | Both attempts visible; unresolved intermittency is not silently passed. |
| Process interrupted or required case omitted | Interrupted/incomplete presentation, never green from an empty failure count. |
| Two runs or variable groups execute | Distinct results and evidence; no overwrite or mixed report. |
| Existing foundation, different issue tracker or no MCP | Reuse the foundation and adapt reporting; missing optional recommendations do not block browser QA. |
| Two journeys use the same login controls | Both import the canonical verified helper; no duplicated selector definitions or per-run rediscovery. |
| Locator resolves multiple controls after a UI change | Preserve the failure and investigate scope; never silently use the first match. |
| Verified new locator or accepted helper repair | Update the application's KB entry with scope, revision, and evidence; subsequent steps can read it. |
