[← platform / frontend-chat](index.md)

# PLAT-293 — `window.report` can only write DB state, never trigger a chat message or a run

| Field | Value |
|---|---|
| State | deployed |
| Priority | - |
| Product | platform |
| Area | frontend-chat |
| Summary | now has a local `window.report.sendChatMessage` implementation: a host message review panel, the existing Ask in chat queue, an explicit new-chat choice, queued/cancelled receipts, and per-view duplicate protection. |

| Coordination | Value |
|---|---|
| Assigned agent | Claude Code |
| Ticket state | `chat capability implemented locally; iframe isolation remains separate work` |
| Last synchronized | `2026-09-05` |

## 2026-09-05 — Report-to-agent chat capability implemented locally

The user clarified the desired scope: let an existing report save an approval,
then offer a specific message to the workflow agent, using the same existing/new
chat behavior as the human-decision panel's Ask in chat. No replacement decision
store or direct scheduler/route-run API is needed for this capability.

- `window.report.sendChatMessage(message, { requestId? })` now opens a native
  app dialog outside the report frame. It shows the host workspace and editable
  message, with **Start a new chat** and **Send to agent**. No message is sent
  until the user submits. Synthetic DOM submissions are ignored.
- The shared `sendWorkflowMessageToChat` helper reuses the usual interactive
  workflow chat, queues behind a foreground turn, and creates a builder chat
  when none is suitable. The new explicit `newChat` option creates one even if
  another is running. Scheduled, bot, view-only, and other-workflow chats are
  excluded from reuse. Appending a request preserves the existing queue and
  the user's unsent chat draft.
- Results distinguish `cancelled` from `queued`, including the tab, reuse, and
  running-turn queue flags. They do not claim execution started or completed.
  Pending double clicks share a request; repeated Send clicks are guarded.
  An optional item/version/action ID deduplicates successful identical requests
  in the current report view, bounded to 100 receipts. Dispatch failure stays
  retryable in the dialog; closing/changing the report cancels unsent requests.
- The method is exposed through the common host runtime. It rejects before
  initialization instead of replaying a chat action on render, and the headless
  preview explicitly rejects it. Static publishing guidance denies it too.
- Reporting, design, improvement, and report-plan guidance documents the API,
  save-before-send ordering, exact item/consumer context, cancelled vs queued
  statuses, retries without another approval write, and consumer-side rechecks.
  Existing report HTML adopts the API in its action handlers; there is no blind
  automatic rewrite of report-owned approval buttons.
- Follow-up skill audit confirmed these references are materialized into
  `builder-reference` and `workflow-commands`. Updated the skill/registry
  descriptions so report-to-agent actions are discoverable, and clarified older
  wording that could be misread as banning report-owned approval buttons.
  Templates use Go embedding: serving the new guidance requires a rebuilt and
  restarted backend and a fresh skill read/agent attachment; editing source
  alone does not update guidance already held by a running agent.
- Added the canonical `human-in-the-loop` reference to the built-in
  `builder-reference` skill for Workshop/Run, and to execution agents when they
  hold human-feedback, decision, or steering tools. It distinguishes all seven
  interaction patterns, existing authorization, unattended runs, response
  expiry, decision/dispatch/application states, and report approval consumers.
  Plan design, execution, report design/improvement, reporting policy, and the
  tool reference point to it. Fixed stale guidance claiming urgent response
  cards fan out through external notification channels. Attachment and
  capability-selection tests cover discovery; the guidance suite passes.
- Audited the Builder system prompt and added a compact human-in-the-loop
  skill pointer shared by API/coding-CLI transports and Workshop/Run modes.
  Removed the unconditional inline-answer instruction; known-user answer
  requirements and full-workflow launch semantics live in the referenced skill.
  Kept the system prompt lean with one human-interaction pointer, removing
  duplicated response-card, approval, report-API, and notification details.
  Removed the retired
  `builder/improve.html` decision destination and nonexistent step-reference
  names. Rendered-prompt/transport, reference-availability, prompt-size, and
  server prompt-assembly checks pass. The known lower-level skip-input
  default-approval fallback is not repaired by these prompt changes.

This implements a user-reviewed **chat request**, not atomic approval-and-run,
durable exactly-once execution, or a typed approval-consumer dispatcher. The
existing same-origin iframe access remains unresolved; the dialog and trusted
event guard do not establish an isolation boundary. The earlier review below
is retained as history and the separate isolation requirement remains open.

Validation: focused frontend controller, queue-routing, bootstrap, and native
dialog tests; TypeScript and production build/bundle budget; guidance rendering.
An in-app browser fixture exercised the real iframe host and dialog through
approval-save → existing-chat queued receipt and explicit-new-chat receipt using
simulated DB/dispatch operations. No live approval was changed or workflow work
triggered during validation. Changes are local, not pushed/deployed.

## 2026-09-05 — Code review: capability gap confirmed; proposed API is not an isolation fix

- Confirmed: `ReportDataApi` and `installReportHost` expose no chat enqueue or run trigger. The websiteaeo report's click handler writes `audit_findings.status` and refreshes its display; it does not dispatch work.
- The existing chat helper queues behind an active foreground turn. A chat submission therefore means **queued**, not proof that a workflow step started or finished. UI receipts must distinguish these states.
- A generic full-workflow/Pulse trigger is not equivalent to implementing the approved finding. The checked websiteaeo plan begins with collection/audit and still contains an `approve-fixes` human-input step before `implement-approved-fixes`. Resolve an explicit, workspace-scoped consumer action and its approval record; do not rerun the entire audit or ask for the same approval again merely to consume a report decision. Re-read and validate the decision at execution time, and deduplicate repeat clicks/requests.
- The iframe concern is confirmed in source: `HtmlWidgetFrame.tsx:297` uses `allow-same-origin allow-scripts`, host injection accesses the frame directly, and the app stores its auth token in localStorage (`api.ts:516`). Adding a sanctioned API and a host confirmation UI does **not** remove the report's existing same-origin access or prevent bypasses. Isolation is separate required work: an opaque-origin or isolated-origin frame with an explicit message bridge and host/backend authorization. Removing `allow-same-origin` alone would break the current direct-injection/theme/file bridge and needs compatibility tests.
- Do not rely on report-authored confirmation text as authority. The host should resolve and display the actual workspace, action, target decision, and dispatch result. A generic free-form chat request must remain distinct from a typed approval-consumer action.

Review was source-only and inspected the local websiteaeo fixture. No report code was executed, credentials read, messages sent, runs triggered, or security bypass attempted. No implementation/deployment is claimed.

## 2026-09-05 — Report approve/reject buttons cannot act immediately, only queue for a later run

### Observation

Interactive HTML reports (`db/reports/index.html`, rendered in the sandboxed
iframe via `window.report`) can only read data and write a single DB
cell/row: `query`, `get`/`getText`, `getHtml`, `fileUrl`, `openFile`,
`renderMarkdown`, `updateField`, `updateFields`
(`frontend/src/components/workflow/reportWidgets/reportHostRuntime.ts`,
backed by `reportEmbedContext.tsx` and `POST /api/report-field` —
`workspace/handlers/query.go:1112`, `UpdateReportField`). There is no
`sendChatMessage`, `runWorkflow`, `executeStep`, or `triggerRun` on that
surface.

The concrete pattern this blocks: `websiteaeo`'s report renders Approve/Reject
buttons per SEO/AEO finding that call `window.report.updateField('audit_findings',
id, 'status', action)`. The write lands in `db/db.sqlite` immediately, but
nothing acts on it until the next scheduled run's `implement-approved-fixes`
step re-checks `audit_findings.status`. A user who wants a fix applied right
now has no button that does that — only "approve, then wait for the next cron
tick" (`30 9 * * 1,5` in this workflow's `workflow.json`).

A functionally equivalent "click → send a chat message now" mechanism does
already exist in the codebase — `sendWorkflowMessageToChat`
(`frontend/src/utils/reportHumanInputChat.ts:158`) enqueues into the same
durable per-tab queue `ChatInput` uses, firing immediately on an idle chat.
But it belongs to `ReportHumanInputPanel.tsx`, a native React component
rendered as a sibling **above** the sandboxed report iframe
(`ReportViewer.tsx:189`), not inside it — report-authored HTML/JS has no
documented way to call it. Likewise `POST /api/scheduler/jobs/{id}/trigger`
(`scheduler_routes.go:895`, `TriggerNow`) is a genuine run-now endpoint, but
it is wired only to the Scheduler UI, never to reports.

### Why this is a platform gap, not a workflow authoring choice

No workflow can build "approve in the report → executes now" today because
the only report-reachable primitive is a DB write; the pieces that would
close the loop (chat enqueue, run-now trigger) exist but are not exposed on
`window.report`. This is exactly the same shape as PLAT-256 (`window.report`
was read-only until `updateField`/`updateFields` were added) — the write
surface needs one more capability tier.

### Proposed fix (not implemented)

Add `window.report.sendChatMessage(text)` and/or `window.report.triggerRun()`
to `ReportDataApi`/`installReportHost`, proxying into the existing
`sendWorkflowMessageToChat` and `TriggerNow`/`TriggerPulseNow` calls that
`ReportViewer.tsx` already has access to. The open design question is trust,
not plumbing: report HTML is frequently agent-authored and not
human-reviewed before publish, so a button that can unilaterally start a real
workflow turn or post a chat message as the user needs an explicit
consent/confirmation model (e.g. a host-rendered confirm step outside the
sandbox, not a bare JS call the report can fire silently).

### Related, worth folding into the same fix

Because the report iframe uses `allow-same-origin` on `srcdoc`, its origin is
the parent app's origin, so report JS can already reach the parent's
`localStorage` bearer token and call *any* backend endpoint directly via raw
`fetch()` — including `/api/scheduler/jobs/{id}/trigger` — bypassing
`window.report` entirely. This review did not establish whether any shipped report uses that path.
The sandbox therefore does not establish isolation from the parent app.
A sanctioned API with a consent gate is useful capability design, but it
does not close this bypass; the isolation/bridge work above is required.

### Verification (not started)

No test/build/deploy evidence yet — this is a design proposal from a chat
discussion, not a landed change.

### Related preview failure investigated — 2026-09-05

The Hetzner agent's report that `preview_report` remained loading was investigated separately from this ticket's action API. Commit `822d10ea7` (2026-09-04, “Add preview_report: a true-render check for the workflow report”) introduced both the build output under `agent_go/cmd/server/static` and a server handler reading `./static/report-preview.js`. The running local server's cwd is `agent_go`, so it expects `agent_go/static/report-preview.js`. Before repair, the preview page returned 200 but the runtime returned 404. The commit's own verification notes exclude the real browser render; package tests run from `cmd/server`, where the original output happens to be found.

Local repair builds into the running server's static directory and copies the asset to the package-test and frontend distribution locations. Rebuild changed the running server's runtime response to 200 JavaScript without restarting it. The source page handler now reports an explicit failed state/503 when the runtime is missing, covered by a regression test; that backend change requires the next server rebuild/restart. The later `12479bdaf` commit only added a frontend test dependency among the preview-related paths and did not cause this mismatch.

The actual Hetzner report was captured successfully through the in-app browser. The opt-in headless E2E attempt stopped with `workspace execution authorization required`; full `preview_report` success is therefore not claimed. Retry from an authorized normal workflow session. This does not implement PLAT-293's report action proposal.

## 2026-09-17 — Notification delivery ownership integration

The [PLAT-106 notification-ownership follow-up](plat-106.md#notification-ownership-follow-up--2026-09-17) also affects the shared queued-message drain used by report-to-chat dispatch. Once the report helper selects an interactive target and queues a request, delayed submission now retains that target's tab/session even if the user selects another chat. Closed/reused tabs cannot redirect the pending message; a rejected submission restores it only to its original queue.

The five new queue-effect tests cover target selection changes, closure/reuse, rejection, and replacement-session isolation. They do not constitute a report-dialog or iframe end-to-end test. Report authorization, confirmation, deduplication, and the unresolved iframe-isolation work remain under this ticket with their existing status. Live report acceptance should queue a confirmed request in Chat A, select Chat B before delivery, and verify the request runs only in A.

Follow-up status: **implemented and tested locally; not deployed; runtime re-verification pending**. PLAT-106 remains the canonical implementation/test record. This note does not close the original ticket or change its assigned agent.

## Register notes

[PLAT-293](plat-293.md) now has a local `window.report.sendChatMessage` implementation: a host message review panel, the existing Ask in chat queue, an explicit new-chat choice, queued/cancelled receipts, and per-view duplicate protection. Report authors can save an existing approval before offering the scoped action message. This is a chat capability, not an atomic approval-consumer dispatcher. The iframe's existing same-origin access remains separate isolation work. Tests/build and simulated browser interaction passed; not pushed/deployed.

2026-09-05 local PLAT-259 follow-up: the plan canvas gives major routes separate wider columns and adds click-to-trace with faded unrelated nodes/edges, keyboard clearing, and Fit plan to view. Verified against Build in Public locally; see [PLAT-259](../../goals/step-execution/plat-259.md). Not deployed; live ticket counts unchanged.
