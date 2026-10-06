[← platform / frontend-chat](index.md)

# PLAT-279 — Workflows were building a step just to fill the activity tab, next to run summaries that already had the same facts

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | platform |
| Area | frontend-chat |
| Summary | reporting policy, severity medium — a required report tab |

| Coordination | Value |
|---|---|
| Assigned agent | Claude Code |
| Ticket state | `implemented; migration unexercised` |
| Last synchronized | `2026-09-03` |

- **Priority:** reporting policy, severity medium — a required report tab
  with no sanctioned data source made builders invent one, adding plan
  steps and tables that carry no domain value.
- **Origin:** raised by the user reading the reporting policy: "we have
  notify.. where we notify to the org run summary right", then "right now
  for daily actions workflow are adding custom steps.. which might not be
  required always", and finally the framing that settled it — "we can tell
  the agent about default run summary.. if user is happy with that.. we can
  use that in daily actions.. if user want custom that his choice".

## Problem

Since 1.0.30/1.0.31 every report must carry one top-level tab answering
"what did this workflow actually do" (`Daily Action` / `Recent Activity`).
The policy said to read it from `db/db.sqlite` but never said *what* to
read. A workflow whose own domain tables do not naturally hold a per-run
activity narrative therefore had to manufacture one — an extra step, an
extra table, or extra logic inside an existing step, existing solely to
feed a report tab.

Meanwhile every `notify_user(notification_kind="run_summary")` call — which
`pulse-finalizer.md` already requires at the end of a Pulse cycle — was
writing exactly that narrative, structured (title, status, message,
fields, sections, timestamp), into `org_dashboard_notifications` in the
workflow's **own `db/db.sqlite`**: the same file the report queries.
`OrgDashboardConnector.IsEnabled()` returns true unconditionally, so this
happens whether or not any Slack/Gmail/WhatsApp channel is configured, and
the table keeps the last 50 rows per kind.

Nothing in the reporting docs mentioned that table. So the same facts were
authored twice, by two different paths, with no rule keeping them
consistent — and the second authoring cost a plan step.

## Resolution

**Policy** (`reporting-policy.md`, `design-reporting-ui.md`,
`improve-report.md`): the activity tab's default source is
`org_dashboard_notifications` where `notification_kind = 'run_summary'`,
ordered by `created_at desc`. It needs no step, table, or column. Its
`message` is agent-written markdown and goes through
`window.report.renderMarkdown`, not printed raw. An empty table renders
"no runs recorded yet", not an error. A bespoke activity view is what the
parent asks for, not what the agent assumes; `improve-report.md` item 8
now also flags a step or table that exists *only* to feed this tab as
unnecessary complexity.

**Migration** (contract `1.0.36`,
`upgradeActivityTabFromRunSummary`): a one-time turn that reads the
report, works out what feeds the activity tab, and branches — already
reading run summaries or real domain tables, nothing to do; bespoke
machinery with no other consumer, tell the parent plainly and *ask*
whether to switch and retire it; cannot tell, leave it and say so. It
stamps 1.0.36 whichever way the parent chooses, including "keep what we
have", and deletes nothing without their agreement in that conversation.

## Verification

- `cmd/server` suite green, including the contract-plan tests whose
  expected migration counts and final-step assertions moved with the new
  version (one of them carried a second `len(plan)` check inside the same
  condition, which is why the count had to be fixed in two places).
- The guidance templates render.
- Not exercised: no workflow has run the 1.0.36 migration yet, so the
  branch that asks the parent about bespoke machinery is unobserved.

## Remaining

- The migration's judgment call — "does this step exist only for the
  tab?" — is the agent's, from reading the report and plan. A workflow
  with an ambiguous setup is expected to take the third branch and leave
  it alone; whether it actually does is worth watching on the first few
  real upgrades.
