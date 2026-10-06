[← platform / integrations](index.md)

# PLAT-264 — Make Org Dashboard a durable notification provider instead of parsing stale Builder cards

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | platform |
| Area | integrations |
| Summary | P1 product-truth and observability boundary. |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | `parent-to-child notification handoff fixed; server verification passed; live scheduled-run/UI reverify pending` |
| Last synchronized | `2026-09-04` |

- **Priority:** P1 product-truth and observability boundary.
- **Related:** [PLAT-018](../../goals/pulse-governance/plat-018.md)), [PLAT-083](../../goals/pulse-governance/plat-083.md)),
  [PLAT-085](../frontend-chat/plat-085.md)).
- **Origin:** The user observed that the Organization page still read
  `builder/card.health.html`, `builder/card.progress.html`, and
  `builder/card.cost.html`, although current workflow/Pulse paths no longer
  reliably generate those files. The page therefore presented old data as
  current truth.

## Problem

The platform had two unrelated output paths:

1. `notify_user` delivered current workflow and Pulse summaries to Gmail,
   Slack, and WhatsApp;
2. Org Dashboard parsed three optional workflow-owned HTML fragments.

The second path had no producing-run guarantee, no typed contract, and no
durable history. Missing cards could look empty while old cards could look
healthy indefinitely. Adding another dashboard-specific agent tool would have
created a second summary call and allowed external notifications and the Org
view to disagree.

## Decision

Treat Org Dashboard as the fourth `notify_user` provider:

- **Gmail**, **Slack**, and **WhatsApp** remain configured external delivery
  providers.
- **Org Dashboard** is an always-on internal provider for workflow-scoped
  `run_summary` and `pulse_summary` notifications.
- One channel-neutral summary is the shared truth. Channel-specific renderings
  may add presentation, but may not contain facts absent from the neutral
  summary.
- Ordinary `general` notifications are not persisted to the Org Dashboard.

This supersedes the card-parsing portion of
`docs/workflow/org_dashboard_design.md`; it does not restore card generation
and does not introduce an `update_org_dashboard` tool.

## Implemented contract

### Notification boundary

`notify_user` now accepts:

- `summary_title`
- `summary_status` (`completed`, `failed`, `blocked`, `waiting_for_user`,
  `waiting_for_platform`, `monitoring`, `informational`, or `no_run`)
- `summary_fields`
- `summary_sections`

The backend carries this typed, channel-neutral content together with the
trusted workflow workspace path. Existing Slack/Gmail compatibility fallback
remains so older structured callers still produce a useful neutral summary.
Pulse finalizer guidance requires the neutral fields for run and Pulse
summaries.

### Durable provider

The always-enabled `org_dashboard` connector:

- receives the same `notify_user` fan-out as external providers;
- persists only classified run/Pulse summaries in the workflow's existing
  `db/db.sqlite`;
- keeps 50 records per summary kind, independently, so frequent run summaries
  cannot evict the latest Pulse summary;
- preserves the latest run and Pulse rows even when the bounded recent-history
  query contains only one kind;
- is independent of external-provider availability or delivery failure.

### Read API and UI

`GET /api/org-dashboard/notifications` reads multiple workflow stores in one
request and returns the latest run summary, latest Pulse summary, and recent
history for each workflow.

The Organization page no longer reads or parses the three `builder/card.*.html`
files. It renders the durable summaries, distinguishes success, neutral
updates, warnings/failures, missing notifications, and pending human decisions,
and exposes structured fields/sections in workflow details. Existing stale
cards are intentionally not migrated because their freshness cannot be proven.

## Main implementation files

- `agent_go/cmd/server/services/org_dashboard_connector.go`
- `agent_go/cmd/server/services/notification_content.go`
- `agent_go/cmd/server/services/notification_destination.go`
- `agent_go/cmd/server/virtual-tools/human_tools.go`
- `agent_go/cmd/server/org_dashboard_notifications.go`
- `agent_go/cmd/server/server.go`
- `agent_go/cmd/server/guidance/templates/system/pulse-finalizer.md`
- `frontend/src/components/org/OrgDashboard.tsx`
- `frontend/src/services/api.ts`
- `frontend/src/services/api-types.ts`

## Verification completed

- Connector persistence tests cover classified summary storage, ignoring
  general notifications, and retrieving both latest kinds independently of
  the recent-history limit.
- `notify_user` tests prove the channel-neutral content and workflow path reach
  the provider contract.
- Focused Go service, virtual-tool, and server tests pass.
- Frontend TypeScript build and targeted ESLint checks pass.
- `git diff --check` passes for the implementation files.

## Runtime acceptance still required

After restarting with this build:

1. Run one normal scheduled workflow that calls
   `notify_user(notification_kind="run_summary", ...)`.
2. Run one Pulse finalizer that calls
   `notify_user(notification_kind="pulse_summary", ...)`.
3. Confirm configured Gmail/Slack/WhatsApp delivery remains truthful and the
   same workflow shows the two current summaries in Org Dashboard.
4. Disable all external channels and prove Org Dashboard still records the
   classified summary.
5. Confirm editing an old `builder/card.*.html` file cannot change the Org
   Dashboard.

The ticket becomes `done` only after that producing-run/UI verification.

## 2026-09-02 runtime acceptance failure and repair

The first producing-run check exposed a bridge-only defect that the original
direct-context test did not exercise. `notificationDestinationFromQuery`
correctly supplied the trusted `WorkspacePath`, but
`RegisterSessionNotificationDestination` omitted that field while copying the
destination into the session registry. Custom-tool HTTP requests retain the
trusted session ID rather than the original Go context, so `notify_user`
recovered a destination with no workspace path. Gmail could still deliver,
while the internal provider repeatedly failed with
`org_dashboard requires a workflow workspace path`; every workflow therefore
appeared as `Awaiting first run` even after real runs.

The registry now preserves a non-empty `WorkspacePath`. A regression test
uses only the session ID in the tool context and proves that a classified
summary recovers `Workflow/demo` across the same registry boundary used by the
MCP bridge. Existing empty dashboard stores are not populated by this code
repair; live re-verification still requires one new typed summary per workflow
or a separately approved truthful backfill.

## 2026-09-04 — Explicit Activity status contract

The first Org view treated every latest `pulse_summary` as an update while the
all-workflows grid inferred attention only from `run_summary.status`. The
headline count used both Pulse and run warnings, so an open Pulse action could
be counted without being presented as a clear current task. Warning color is
not a reliable action model: a warning can describe a fixed issue, a monitor,
or a real blocker.

Each classified summary has one semantic `summary_status`: `completed`,
`failed`, `blocked`, `waiting_for_user`, `waiting_for_platform`, `monitoring`,
`informational`, or `no_run`. Its title, message, facts, and sections explain
the why. There is deliberately no secondary state, owner, or next-action
model. Attention statuses are shown once in **Needs attention**;
completed/monitoring/informational Pulse work appears in **Pulse updates**;
pending human inputs remain in **Decisions required**. The renamed **Activity**
page shows compact facts directly and exposes retained run/Pulse history both
in the page and per workflow. Older severity values map conservatively to the
new semantic statuses when read.

## 2026-09-04 — Scheduled child sessions lost the trusted notification destination

A live `rtslatency` scheduled run exposed a second workspace-context boundary.
The scheduled parent session had the correct backend-owned notification
destination, but workflow execution changed identities twice before the digest
called `notify_user`:

1. the schedule session created a workflow group MCP session;
2. the `daily-sprint-progress-digest` message-sequence step created its own
   execution/tool session.

The folder guard and parent-chat metadata were propagated across these
boundaries, but the notification destination registry was not. Consequently,
the child recovered an empty `WorkspacePath` and
`notify_user(notification_kind="run_summary")` failed only for Org Dashboard
with `org_dashboard requires a workflow workspace path`. A retry that supplied
`X-Workspace-Path: Workflow/rtslatency` failed correctly: notification routing
is trusted backend state and must not be authorized by an agent-controlled
header. Slack could succeed only when retried from a session that still owned
the destination, which made the failure look connector-specific even though it
was a session-handoff defect.

The registry now has an explicit clone-based parent-to-child inheritance
operation. Both workshop and batch group creation inherit the originating
destination, and every sub-agent/message-sequence tool session inherits from
its group. This carries the complete trusted contract together — workflow path,
workflow/user identity, route selections, Slack/WhatsApp/Gmail destinations,
summary channels, recipients, connection IDs, and webhooks — without sharing
mutable pointers between sessions. Copied registrations are removed when group
and message-sequence runtimes close.

### Regression coverage

- Registry coverage proves a child receives the complete destination and owns
  an independent clone.
- `notify_user` coverage executes with only the child session ID and proves the
  Org Dashboard connector receives `WorkspacePath: Workflow/demo`.
- Orchestrator coverage proves `configureSubAgentSessionGuard` performs the
  inheritance used by real message-sequence execution.
- `go test ./cmd/server/virtual-tools
  ./pkg/orchestrator/agents/workflow/step_based_workflow` passes.
- `go test ./cmd/server` passes.
- `git diff --check` passes.

This repair is implemented locally but is not yet deployed. Runtime acceptance
still requires one new scheduled child-step summary after deployment, followed
by confirmation that the same summary appears in Org Dashboard and its
configured external destinations without a fallback send from the parent.

## Decision history

- **2026-08-31 — New provider, not a new tool.** Reused `notify_user` fan-out so
  one agent call owns one summary across all four destinations. This avoids
  duplicate generation and contradictory channel truth.
- **2026-08-31 — No legacy-card migration.** Old HTML has no trustworthy
  generation timestamp or producing-run link, so importing it would preserve
  the stale-data defect the ticket exists to remove.
- **2026-09-04 — Inherit trusted routing; do not trust request headers.** Child
  tool sessions receive a backend-cloned notification destination from their
  registered parent. Agent-supplied workspace headers remain non-authoritative.
