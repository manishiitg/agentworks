## Workflow/Goals Dashboard rules

<!-- dashboard:shared -->

### Workflow-specific authoring and access

Workshop owns Dashboard authoring under `db/reports/`; keep Dashboard-only changes
presentational unless workflow behavior or evaluation changes were requested.
Run mode never authors Dashboard pages; it produces their durable data/assets.
Workflow viewers, including read-only readers, may run read-only Dashboard scripts
under the viewer's current MCP/Vault permissions. Dashboard access grants no
credentials. External dashboard authoring requires `dashboards:read` and
`builder:chat` consent with its companion read/run scopes and Builder enablement.

For requested animation or MP4 explainers, read
`read_skill(skills=[{"name":"builder-reference","path":"references/motion-guide.md"}])`.

### Workflow activity and route identity

When displaying route-specific activity, identify runs
by `(routing_step_id, route_id)`
from `route_summaries_json` in the same notification row. Parse that JSON
array and render each entry's label, title, status, message, fields, and
sections; do not merge same-named routes or attribute shared facts to a
specific route. `branch` choices are not separate
sub-workflows. Use the same grouping for any Pulse-summary history.
Check the real schema first: absent table/column or older rows use the old
message and explicit legacy Route field, with scope shown as not recorded
when absent. Never backfill route identity from prose, show an unreviewed
route as clean, or copy a workflow total into each route. No extra plan step
or workflow-owned activity table is needed.

### Platform decisions

Dashboard-owned approval buttons are supported: update an existing business row
(an email's `status`, an audit finding's approval), then optionally offer a
scoped agent request using `sendChatMessage` below. This does not replace the
platform's human-decision lifecycle. Platform decisions created through
`create_human_input_request` retain their options, answers, consumption, and
audit trail in the Pulse panel/chat; do not edit `report_human_inputs` through
the business-field write API or invent a duplicate platform decision store.

### Sending a Dashboard request to the workflow agent

When choosing between a business approval, a Pulse decision, a blocking
checkpoint, and an agent request, read
`read_skill(skills=[{"name":"builder-reference","path":"references/human-in-the-loop.md"}])`.
The API details below implement the Dashboard-to-chat pattern.

`await window.report.sendChatMessage(message, { requestId })` sends directly
from the Dashboard action, without a second popup or chat-choice step. The app
uses the same workflow-scoped chat queue as the human-decision panel's
**Ask in chat**: reuse an interactive chat,
queue behind its running foreground turn, or create a chat if none exists.
Scheduled/view-only/bot tabs are excluded. This sends a conversational request;
it does not directly execute a route or trigger the scheduler.

Call this only from a user action handler. Never call it from `ready`, render,
polling, or refresh handlers. It is unavailable in `preview_report`. Before the
host initializes, it rejects instead of queuing a request to replay on load.
The host supplies the workspace; Dashboards cannot choose another workspace.
Messages must be non-empty and at most 12,000 characters.

For an existing Dashboard-owned approval, await the DB commit before offering the
action request. Include the exact row/proposal version and the intended route
or consumer step. Ask the agent to re-read the current approval, skip work
already applied, and act only on that item. Do not use a generic “run workflow”
message when it would repeat collection/audit or another approval gate.

```js
// In a user click handler, with the button disabled until finally.
await window.report.updateField('audit_findings', row.id, 'status', 'approved');
showStatus('Approval saved. Sending action request…');
const result = await window.report.sendChatMessage(
  `Apply only approved audit_findings row ${row.id}, proposal version ${row.proposal_version}. ` +
  `Re-read its current approval and proposed fix from the database; skip it if already applied. ` +
  `Use the existing remediation route for this item, then verify and refresh the Dashboard.`,
  { requestId: `finding:${row.id}:${row.proposal_version}:apply` }
);
showStatus(result.queuedBehindRunningTurn
  ? 'Request queued behind the current chat turn.'
  : 'Request queued in chat.');
```

Use real schema fields/versions and actual route or step IDs, never copy
placeholder names into a workflow that lacks them. The successful result is
`{ status: 'queued', tabId, reused,
queuedBehindRunningTurn }`. Queued is not proof that work started or completed;
show applied/verified outcomes only from fresh execution evidence.

The approval write and chat enqueue are separate operations. A failed enqueue
does not undo the saved approval (a later scheduled consumer can still read it).
On failure, keep that approval visible and offer **Send action request** again
without rewriting it. Catch errors locally and re-enable the
button in `finally`. Repeated clicks while sending share one request;
an optional stable `requestId` (max 200 characters) reuses a successful receipt
for the same message in the current Dashboard view (up to 100 receipts). Reloads,
different views, and later sessions still require the consumer's durable
already-applied check. This API does not provide atomic approval-and-execution.

For typed route rows, `summary_text` is the shared lead and `message` the complete
legacy digest. Render lead plus route entries once, or the complete message as a
fallback, never both.

### Goal progress: shared data and an optional ready-made widget

Dashboards can show the same primary metric, supporting metrics, and measured history
as Pulse. No additional collector, table, chart library, or custom CSS is needed:

```html
<section id="goal-progress"></section>
<script>
window.report.ready(async function () {
  await window.report.renderGoalProgress('#goal-progress');
});
</script>
```

The optional widget renders primary/supporting cards, targets, change since the
previous measurement, trends, freshness, and expandable definitions/evidence/history.
It inherits the Dashboard's text color, fits the available width, and replaces its
contents on refresh. Use an empty `div` or `section` as the container. Keep the
returned promise inside `ready` so preview_report can observe loading/errors.

For a custom layout, `await window.report.getGoalMetrics()` returns
`{ metrics, observations, progress }`. Each progress item contains `metric`,
`current`, `delta`, `state`, `stale`, `targetMet`, `latest`, `history`, and `numeric`.
`current` can be undefined: never turn missing data into zero. The latest failed
measurement remains unavailable rather than falling back to an older good value.
History includes up to 120 comparable observations per active metric, with scope,
unit, and evidence preserved. Use `window.report.query` against
`workflow_goal_metrics` and `pulse_goal_observations` for a longer/custom history;
check `sqlite_master` first for workflows that have not configured measurements.
These are platform-owned, read-only tables for Dashboards; use the managed goal tools
for changes, not `updateField`/`updateFields` or direct SQL writes. Run `/setup-goals`
when definitions or collection are missing. Do not invent targets or samples.

Adding this section is optional and does not replace the Dashboard's own navigation
or layout. Offer it when a user wants goal tracking in their dashboard. Validate
and preview the Dashboard using the normal Dashboard tools after adding it.
