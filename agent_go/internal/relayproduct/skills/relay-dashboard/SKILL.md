---
name: relay-dashboard
description: Create or update a Relay Dashboard using the shared HTML runtime, project data and read-only scripts.
external: true
external_description: "How a Relay's dashboard is authored and fed with recorded run data."
external_scopes: ["relays:write"]
external_note: "External mapping: dashboard action=list|get|link|create|update|validate|preview|publish|restore uses the shared revisioned dashboard handlers and existing project authoring consent. Read/write Relay helper source through files when allowed, or delegate broader changes through builder action=chat. Internal Relay bridge variables and client URLs are server execution context, not externally usable credentials."
---

# Relay Dashboard usage

Use the shared Dashboard contract for HTML authoring, project-relative file
attachments, inline documents, data, scripts, validation and publication.

<!-- dashboard:shared -->

## Relay-specific rules

- Open the **Dashboard** button in Relays; Graph remains the default view.
- The managed database is optional. Dashboard authoring does not add workflow
  plans, Pulse or automatic DB/KB/learnings closing stages to Relay execution.
- Scripts are independent of `relay.py`. They do not get the Relay SDK `ctx`,
  start a published Relay or invoke `ctx.call_agent`. Do not run a Relay with
  external effects during Dashboard load or refresh.
- Read recorded invocations with
  `await window.report.getRelayRuns({ version: 'draft', limit: 20 })`.
  The response is `{ version, runs }`; each run includes `run`, `run_id`,
  `status`, `attempt`, `started_at`, `duration_s`, `steps` (name/status/reused)
  and `result`. Use an explicit published version such as `v2` to show its runs;
  draft and published histories are separate. Limits are 1–50.
- Render empty/error states accurately. Never enumerate `runs/` through a shell
  or report script; the authenticated reader returns only this Relay's recorded
  summaries/results, without private journals, agent prompts or tool arguments.
- Project-selected MCP tools, secrets and variables remain subject to the
  authenticated viewer's live permissions; Relay access grants no credentials.
- `publish_relay` publishes API source, not the Dashboard.
- External dashboard authoring requires `dashboards:read` and `relays:write`
  or Builder consent, retaining its companion read/run scopes and Builder
  enablement. Existing connection and project bounds still apply.
