# Setup, handoff, and reporting

## AgentWorks builder wiring

Use current plan/config tools; do not edit `workflow.json` directly. Load relevant attached `builder-reference` resources through `read_skill`, for example `read_skill(skills=[{"name":"builder-reference","path":"references/plan-design.md"}])`. Use `plan-design.md` and `step-description.md` for step construction, `skill-management.md` for attachment, `stores.md` for persistence, and `reporting-policy.md` for reports; all live under `references/`.

Configure browser mode through `update_workflow_config(browser_mode=...)`. Prefer `auto` unless the customer's environment calls for `headless` or `cdp`. Use `add_step(type="agent", step={...})` for coherent exploration/judgment and `add_step(type="scripted", step={...})` for the repeatable runner; split at meaningful permissions, persistence, or retry boundaries, not each click. Keep outputs in `validation_schema` and wire `context_output`/`context_dependencies` across steps.

Use `update_step_config` to explicitly attach `agent-browser` to managed-browser steps, preserving relevant existing skills. Workflow-level `add_skills` does not cascade to runtime. Load `browser-usage.md` for managed interaction and `playwright-scripted.md` for suites. Use the installed browser guide, fresh snapshots, and real tab ownership; never save transient refs or bypass managed exploration through raw CDP. Resolve actual tool schemas and deployed capabilities before configuring them.

## Customer inputs

| Input | Resolution |
| --- | --- |
| Application and environment | Stable application ID, exact allowed origin(s), base URL, environment label, and optional build identity source. |
| Authentication | Public browsing, an existing authorized session, or selected secret references. Record roles; never persist passwords, cookies, tokens, or storage-state content in the profile/report. |
| Test data | Allowed create/edit/delete operations, fixture ownership, reset/cleanup method, and any actions outside the authorized scope. |
| Smoke expectation | A named scenario with observable success and its source, such as a confirmed requirement or customer instruction. |
| Execution | Browser mode, Playwright project or explicitly selected equivalent runner, canonical test/code location, and bounded execution limits. |
| Evidence | Retention requirements for video, console logs, network logs/HAR, screenshots, and traces; redaction rules; whether missing evidence blocks readiness. |
| Reporting | Default to the workflow Report tab. External destinations and notification intent are explicit customer configuration. |

Missing nonessential tool recommendations do not block setup. Missing required authentication or the chosen repeatable runner does. If anonymous browsing is valid for this application's agreed smoke case, do not require an account merely because another application needs one. Interactive access can be verified while suite setup remains blocked; report those separately.

## Durable profile contract: browser-foundation/v1

The example JSON is a structural illustration, not runnable customer configuration. Use a versioned profile snapshot at `db/assets/browser-qa/<application-id>/foundation-<revision>.json`, referenced from a durable DB record. If the workflow has an existing canonical application store, adapt it and document the mapping in `db/README.md` rather than introducing competing sources of truth.

Required information:

- Contract version, profile revision, application ID, environment label/base URL/allowed origins.
- Source playbook ID/version and explicit customer overrides.
- Browser mode, available tool choice, and account-role references. Do not fabricate an MCP registration ID from a catalog display name.
- Canonical runner/config/smoke-test references, shared locator-helper references, and locator verification evidence. Store pointers to executable source rather than duplicate selector strings in the profile. Follow [the locator and Playwright guide](locators-and-playwright.md).
- Application-specific knowledgebase note reference covering verified locators and test setup.
- Test-data boundaries and reset/cleanup instructions or references.
- Smoke scenario ID, expected behavior, and expectation source.
- Report configuration and selected delivery intent.
- Evidence policy for video, console logs, network logs, screenshots, and traces, including retention, redaction, and required/optional status.
- Verification status, observation time, tested environment/build identity when known, attempt-scoped evidence references, and unresolved setup issues.

Use `configured`, `ready`, `blocked`, or `needs_review` for profile setup status. `ready` requires verified locator helpers and an observed successful run of the saved smoke test from a fresh context. `configured` means saved but not yet verified. A prior `ready` status is historical evidence, not a guarantee that a later session or preview still works. Each validation run performs fresh readiness checks.

## Knowledgebase persistence

Create or update one application-specific note, such as `knowledgebase/notes/browser-qa-<application-id>.md`, using the existing canonical note if present. Save:

- Verified locator keys and expressions/strategies, semantic purpose, page/frame scope, expected role/state, canonical helper path/symbol, source revision, last verified observation, and evidence references.
- Browser/runner configuration, test source locations and invocation, authentication reference names, fixture/reset/cleanup behavior, smoke expectation and its approved source.
- Known limitations, unverified candidates, stale entries, and links to profile/report/evidence. Never include credential values or raw authenticated session data.

Keep executable locators canonical in test code. The KB is their readable, source-linked documentation; consumers resolve the actual helper rather than execute a copied KB expression. Update verified documentation when helpers change, and mark mismatches stale until reverified. Store chronological raw run results in the DB, not an ever-growing KB transcript.

Configure discovery/setup producers with `update_step_config(knowledgebase_access="read-write", knowledgebase_contribution=...)`, naming the exact note and facts to maintain. Consumers use `knowledgebase_access="read"` and their description identifies the note they must read. Reference the note from the saved profile and confirm it is readable during handoff; do not rely on builder chat memory.

Workflow learnings may link to this note for reusable operating methods, but must not become a competing selector inventory. Enable learning writes only for a concrete reusable procedure with a non-empty `learning_objective`. Application-specific findings belong in the KB, not in the reusable builder skill.

## Reporting implementation

Use the current AgentWorks HTML report contract, not legacy JSON widgets:

1. Inspect the existing `db/README.md`, database schema, and `db/reports/index.html` before changing them.
2. Define a profile record and append-only setup observations in `db/db.sqlite`. Include application ID, revision, observation ID, workflow variable group, status, timestamp, profile path, and evidence references. Add one artifact row per expected video, console log, network log, screenshot, or trace with attempt ownership and capture/validation/redaction status. Use actual platform execution identifiers and distinct attempt IDs for retries; never overwrite another group's history.
3. Document keys and idempotent persistence behavior in `db/README.md`. Configure producing steps to write durable rows and `db/assets/` files; their `context_output` is a lightweight forward pointer, not the durable report source.
4. Author or extend `db/reports/index.html` with inline CSS/JS. Gate live queries with `window.report.ready(...)` and load evidence through documented report bridge methods. Escape data when rendering text; never insert untrusted page content as raw HTML.
5. Show the application/environment, setup status, runner readiness, locator verification status, source revision, last verified time, smoke result, unresolved items, and attempt-scoped evidence. Provide video playback plus safe Console, Network, Trace, and Screenshots views. Empty state says setup has not run. Missing evidence must remain visible.
6. Run `validate_report_html()` and verify the report against the real saved observation. Never claim a dashboard has been checked based only on a written HTML file.

The report may live alongside existing views. Do not replace unrelated reporting. A step writes data; it does not regenerate the report document on every run. External Slack/email/issue delivery is optional and requires the intended destination and authorization. Use supported notification/integration mechanisms; the tool recommendation itself grants no authorization.

## Handoff to validation

Critical Journey Validation consumes the versioned profile, browser QA knowledgebase note, approved expectations, and canonical suite/locator helper references. Prefer reuse in the same workflow. For a separate workflow, transfer only authorized non-secret profile/KB snapshots and grant or copy required test sources through supported access; configure required secret references independently. Absolute host paths and another workflow's relative paths do not grant access.

Store whether a profile is unverified or stale, and identify any changes to origin, account role, browser mode, or fixture behavior that require a new readiness check.

## Behavioral acceptance cases

| Fixture situation | Expected behavior |
| --- | --- |
| Reachable app, authorized role, expected smoke result | `ready` with current evidence and a visible report. |
| Required login unavailable or expired | `blocked` with the needed authentication action; no fabricated successful login. |
| Wrong application or unexpected redirect outside scope | Stop affected actions and report the mismatch. |
| Expected smoke behavior is ambiguous | `needs_review`, retaining observation separately from an approved expectation. |
| Saved config but trial not run | `configured`, with no invented verification timestamp. |
| Optional MCP/live-view package missing, selected runner available | Complete the saved smoke test with the supported runner; optional features remain unavailable. |
| Managed exploration works but selected runner is unavailable | Record interactive access separately; setup remains blocked until the repeatable smoke test can run. |
| Two Save buttons or dynamic IDs | Verify a scoped semantic locator; no guessed first match or saved snapshot ref. |
| Fresh context, refreshed page, or relevant rerender | Saved helper resolves the intended control and the smoke assertion still holds. |
| Existing profile/report and valid authorization | Reuse them, preserve history, and request only materially missing inputs. |
| New validation step without discovery chat history | Read the configured KB note and resolve existing test helpers; no rediscovery required. |
| Video succeeds but console or network capture fails | Preserve the smoke result, record the missing diagnostic artifact and reason, and block readiness only when the configured evidence policy requires it. |
