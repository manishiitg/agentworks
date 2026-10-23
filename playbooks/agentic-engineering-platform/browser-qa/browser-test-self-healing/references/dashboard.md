# Self-healing dashboard

Extend the existing browser QA Report page at `db/reports/index.html`; do not create a second dashboard system. Read current `builder-reference/references/reporting-policy.md`, the existing report, and `db/README.md`. Preserve Setup, Runs, Journeys, Findings, and Evidence views from earlier playbooks.

## Durable data

Reuse existing run/case/finding/artifact tables. Add a repair record and repair attempts/decisions only when equivalent storage does not exist.

| Record | Dashboard fields |
| --- | --- |
| Repair | Repair ID, source finding/run/case, application/environment/build, classification, status, profile/journey versions, base/candidate/applied revisions, created/updated time |
| Candidate | Patch path/hash, permitted files/symbols, rationale, assertion-preservation result, confidence/reasons, related journey set |
| Verification | Attempt ID/type, source revision, runner status, original and related case results, cleanup/evidence status, timestamps |
| Decision | Candidate version, approve/reject/defer, reviewer and timestamp from the platform decision lifecycle |
| Artifact | Repair + attempt, original/candidate/canonical phase, `video`/`console_log`/`network_log`/`trace`/`screenshot`, source/format, durable path or null, capture/validation/redaction status and missing reason |

Use stable keys and idempotent writes. Freeze historical source/profile/journey identity. `verified_candidate`, `approved`, `applied`, and `healed` are distinct; derive final state only from the complete recorded lifecycle. The original QA result remains unchanged.

## Layout

Add a **Repairs** section with:

- Summary counts for proposed, needs review, verified, approved, applied, healed, rejected, failed, blocked, and stale-base repairs.
- A filterable repair list showing the original finding, test, classification, current state, age, and next owner/action.
- A repair detail view with original failure versus intended behavior, source-linked diff, affected helpers/tests, policy checks, confidence and uncertainty.
- A verification comparison showing original, isolated candidate, related journeys, and canonical post-apply results.
- Evidence panels for original and verification video, searchable redacted console/network logs, screenshots, trace/capture downloads, and missing/partial/redaction-failed states.
- Approval and publication receipts without claiming that a queued request or saved approval has been applied.
- Knowledgebase status showing last verified entry and the accepted update, when one exists.

Status text and lifecycle explanation must accompany color. Make product bug, environment blocker, repair failed, rejected, deferred, and healed visually distinct. A green healed state requires accepted canonical application and successful canonical rerun.

## Live report behavior

Load all data through `window.report.ready(...)`. Keep current repair/attempt selection on refresh, prevent stale async responses from replacing the selected evidence, escape untrusted application text, and show loading/empty/error/incomplete states. Query against declared expected cases so missing results stay visible.

Only durable `db/` assets are report-visible. Resolve selected videos lazily through `window.report.mediaUrl(path)` into a persistent native video element. Render console/network records as escaped text with level/method/status/type filters; never execute log content or render captured HTML. Never store signed URLs or base64 media, eagerly load every video, expose failed-redaction artifacts, substitute evidence from another attempt, or reset active playback during refresh. Label phase, source, validation, partial status, and audio availability accurately. Follow the shared [evidence capture contract](../../references/evidence-capture.md).

Display a patch through a safe text or markdown view from the durable asset; do not render it as untrusted HTML. Link the exact base/candidate hashes and applied source receipt. An application repository path or KB note reference may be descriptive if it is not directly report-readable.

## Review and actions

The authoritative approval is the durable `create_human_input_request` record linked to the exact repair ID, patch hash, and base revision. The preparation run creates or refreshes it and ends. The Report dashboard and Autopilot decision panel supply **Ask in chat**, **Approve**, **Reject**, and **Defer**. Asking opens discussion without changing the decision; approving stores authority for later and does not apply during the dashboard request. The repair report may expose **Apply approved repair**, **Request investigation**, or **Rerun verification** only through supported user-initiated action controls with stable request IDs. Applying starts the separate action route, which must call `get_human_input_request`, reread current base state, and skip work already applied.

Do not duplicate the platform's approval state with an unrelated report field. Show queued separately from running, verified, applied, and healed. No repair, approval, publication, rerun, or notification occurs during render or refresh. Omit an action when no supported workflow route exists.

## Verification

Run `validate_report_html()`, then inspect the live Report with fixture data for each terminal and intermediate status, source drift, mixed verification results, missing/partial video, missing console/network logs, redaction failure, duplicate requests, empty data, and query failure. Verify video playback, safe log filters, patch readability, run/repair filtering, narrow layouts, keyboard operation, and that original evidence never changes when a candidate succeeds.
