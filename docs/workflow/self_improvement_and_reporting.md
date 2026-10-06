# Workflow self-improvement & reporting — system overview

**Start here.** This is the map of how a workflow keeps itself healthy and moving toward
its goals, and how that work is made visible and steerable for the user. It ties together
five parts that are otherwise documented separately: **Pulse**, its **review and
fix lifecycle**, **scheduled step evidence**, **notifications**, and the **Org dashboard**. Each section
links to the detailed doc.

## Why this is the critical layer — managing at scale

This is the most important subsystem in the product: it's what makes **100+ agents and
automations manageable** by a small team. Without it, every workflow needs a human watching
it — which caps you at a handful. With it:

- each workflow **self-heals** operational breakage and **self-improves toward its goal**
  through Pulse's Workflow Review, Strategy Auditor, Goal Advisor, and Fixer, so routine
  operation needs no human;
- the reporting **rolls everything up** so a human manages **by exception** — the dashboard's
  triage bar surfaces only what needs attention, notifications fire only on real transitions
  (broke / recovered / new finding), and big changes wait as **proposals to approve**. You
  take in a hundred automations at a glance and act only where the system flags it.

Span of control is the whole point: the system handles the routine fixing and improving and
surfaces only the **exceptions + decisions**, so human effort scales with the number of
*exceptions*, not the number of *workflows*. That is the difference between running 5
automations and running 100+.

## 1. Purpose — two jobs

1. **Fix/improve workflows toward their goals.** Keep them *working*, and move them toward
   *winning*.
2. **Make that legible and steerable.** Report what's happening so the user can *see*, get
   *alerted*, and *decide* — because the system proposes big changes rather than applying
   them silently.

Everything below is one of those two jobs, or the substrate that connects them.

## 2. One Pulse system, three kinds of judgment

There is no separate recurring auto-improve control loop competing with Pulse.
Gate selects from three independent reviewers: ordered **Workflow Review** asks
whether the current workflow ran and is built correctly; **Strategy Auditor**
asks what is missing or ineffective inside the selected strategy; less-frequent
**Goal Advisor** searches for a materially different route to the goal. Due
reviewers may run in parallel and never depend on one another's conclusions.

One Fixer then reconciles their findings and applies only bounded safe changes.
Consequential strategy changes remain proposals requiring the existing human
decision flow. One ordered finalizer renders the dashboard, backs up, publishes,
and notifies. The canonical current topology and its rationale live in
[`pulse_consolidation.md`](pulse_consolidation.md).

"Working but off-goal" remains a normal, important state: operational health and
goal progress are separate judgments, and a broken run cannot supply trustworthy
goal evidence.

## 3. The shared substrate (Pulse memory)

Pulse's durable memory is **SQLite**, including module results, reviews, findings,
attempts, verification, finalization, interventions, and comparable goal observations.
`builder/improve.html` is the required lightweight executive journal and
publishable history, not the lifecycle database. It contains Bug/Goal verdicts,
one status sentence, exactly three Latest Pulse cells, and at most six material
Activity transitions. Reviewer coverage, assumptions, findings, checks, fix
attempts, verification, questions, and review evidence remain in SQLite and are
rendered by the Pulse popup.

See `review-improve-log.md` for the journal structure and archive rules.

Pulse also maintains compact **dashboard cards** in the workflow workspace:
- the Dashboard stage overwrites `builder/card.health.html` after module and Fixer outcomes are known;
- Goal Advisor updates `builder/card.progress.html` only when goal status, its active experiment, or its decision materially changes;
- `builder/card.cost.html` remains a compatibility surface for workflows that already publish a separate cost card, while current cost/tool/runtime judgment belongs to Workflow Review.

These are served to the UI by `getBuilderDoc(workspace, "card-health"|"card-progress"|"card-cost")`
(`auto_improvement_endpoints.go`).

## 4. The reporting / steering surfaces

The same verdicts, decisions, and cards Pulse produces while reviewing and fixing are what surface here —
*#2 is #1 seen again*, never a separate analysis.

- **Notifications — `notify_user`** (active, "you need to know this"). Fans out to connected
  channels: **Gmail** (one **inline-styled** `email_html` body; `message_for_user`
  is the automatic plain fallback because Gmail strips `<style>`/`<head>`),
  **WhatsApp**, **Slack**. Deliberately
  **sparing** — only on a decision-worthy transition (broke / recovered / new finding);
  silence on a steady run. Code: `virtual-tools/human_tools.go`, `services/gmail_service.go`.
- **Org dashboard** (passive, "where things stand right now"). The default Org view. It
  enumerates workflows, reads each one's two cards, parses the `data-*` attributes, and
  renders its own React components: a **triage bar** (N need attention + health/goal counts),
  **cards grouped by goal** (🩺+🎯 pills, headlines, "updated Xm ago"), plus loading /
  no-automations / warming-up / error states. Code: `frontend/.../org/OrgDashboard.tsx`,
  `EmployeeDashboard.tsx`. Detailed doc: `org_dashboard_design.md`.
- **Org Pulse** (the org-level daily pass — Chief of Staff). Maintains `pulse/goals.html`
  (the goal scorecard) and `pulse/org-pulse.html` (the dated journal), harvests cross-workflow
  insight into CoS memory, and writes **proposal-only** org recommendations. Code:
  `builtin_schedules.go`. Guidance: `org-pulse.md`, `org-html.md`, `org-goals.md`.

**Oversight is part of reporting:** because big changes are proposal-only, these surfaces are
also where the user *decides* (approve a replan, act on a recommendation), not just a window.

## 5. How it fits together

- **One substrate, two uses:** Pulse verdicts, decisions, and cards drive *both* the fixing
  (harden/replan) and the reporting (improve.html → dashboard → notifications).
- **Cadence:** Pulse runs after producing workflow runs; Gate chooses which reviewers are
  due. Goal Advisor is simply the less-frequent blank-sheet module. Org Pulse runs daily.
- **Ownership (no write contention):** each workflow owns its `improve.html` + cards in its
  own workspace; the CoS owns `pulse/goals.html` + `pulse/org-pulse.html`. The dashboard
  *assembles* at view time — nothing shared is written by two writers.

## 6. Code + docs map

| Concern | Code | Doc |
|---|---|---|
| Pulse loop | `scheduler.go` (`runPostRunMonitor`, `postRunMonitorSteps`) | `pulse_consolidation.md`, guidance `post-run-monitor.md` |
| Review selection and ordered execution | Pulse scheduler + module registry | `pulse_consolidation.md`, guidance `pulse-gate.md`, `post-run-monitor.md` |
| Finding/fix/verification/impact state | Pulse worklist, finding lifecycle, review log, and impact ledger | `pulse_consolidation.md`, `pulse_v2_proof_carrying_architecture.md` |
| improve.html dashboard | Dashboard stage; served via `auto_improvement_endpoints.go` | guidance `review-improve-log.md` |
| Dashboard cards | card writes in loop prompts (`scheduler.go`); `getBuilderDoc` card kinds (`auto_improvement_endpoints.go`) | `org_dashboard_design.md` |
| Notifications | `virtual-tools/human_tools.go` (`notify_user`), `services/gmail_service.go` | (this doc) |
| Org dashboard UI | `frontend/.../org/OrgDashboard.tsx`, `EmployeeDashboard.tsx`, `OrgHtmlPanels.tsx` | `org_dashboard_design.md` |
| Org Pulse (CoS) | `builtin_schedules.go` | guidance `org-pulse.md`, `org-html.md` |
| Monitoring overview | — | `workflow_monitoring.md` |

## Open / evolving
- Dashboard cards are **v1: current status** (rolling trend deferred).
- Attempt-scoped proof eligibility must be generated by the backend, and invalid completed
  reviews must be retained as quarantined evidence rather than discarded.
- Longitudinal impact measurement is now present, but useful conclusions still require
  comparable observations across more workflows and multiple later runs.
