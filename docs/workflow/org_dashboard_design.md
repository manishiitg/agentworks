# Org Dashboard — design

**Status (2026-08-03): historical design record; implementation evolved.** The
card-based Org dashboard was built, but the two-loop writer model and fixed Pulse
sequence below are no longer the current Pulse contract. Read
[`pulse_consolidation.md`](pulse_consolidation.md) for current review/fix
architecture and [`workflow_monitoring.md`](workflow_monitoring.md) for the
current database-native per-workflow Pulse popup. The body below is retained to
explain the Org dashboard's card-assembly decisions.

## Problem
Today the Org page (`frontend/.../org/OrgHtmlPanels.tsx`) just **embeds two HTML docs**
— `pulse/goals.html` (scorecard) and `pulse/org-pulse.html` (the CoS daily journal). It's
*documents to read*, not a dashboard, and it's only as fresh as the **daily** CoS Org
Pulse. You can't see, at a glance, "what is the whole org doing, are we meeting our goals,
and if not what's being done about it."

## Principle
**Bottom-up, assembled at view time.** Each workflow keeps its own status current as it
runs; the Org page **assembles** those into the live dashboard. No central file that
multiple writers share. No separate JSON data model — the agents keep producing readable
HTML (see "Data carrier").

## Two loops = three axes (the core insight)
Every workflow runs two distinct loops; they answer different questions and own different
parts of a card:

| Loop | Cadence | Job | Axis | Statuses |
|---|---|---|---|---|
| **Pulse** | per run | **fix** — keep it working (triage → low-risk fix → backup → notify) | 🩺 Operational ("is it working?") | healthy / bug / critical |
| **Pulse report step** | per run | **report** — surface spend and elapsed-time telemetry without optimizing | 💵 Cost/time ("is it spending sanely?") | normal / elevated / missing |
| **Auto-improve** | scheduled | **improve** — move it toward its goal long-term (experiments, replan, harden, db/KB/learnings) | 🎯 Goal progress ("is it winning?") | on-track / at-risk / off-goal |

"Working but off-goal" is a normal, important state (runs fine, not moving the goal) — the
dashboard must surface it. The user's "are we meeting goals + what steps" lives mostly on
the 🎯 Auto-improve axis; Pulse is the keep-the-lights-on axis underneath.

## Writers & ownership (no shared file)
Many writers (every workflow's two loops + the CoS), so each owns **distinct** files:
```
pulse/
  goals.html                    ← CoS Org Pulse (goal scorecard)            [daily]
  org-pulse.html                ← CoS Org Pulse (dated journal)             [daily]
  cards/
    <workflow>.health.html      ← Pulse loop        (🩺 + last fix + trend) [per run]
    <workflow>.cost.html        ← Pulse report step (💵 + spend/time)       [per run]
    <workflow>.progress.html    ← Auto-improve loop (🎯 + improvement)      [per improve run]
```
A workflow's Pulse only touches `*.health.html` and `*.cost.html`; its Auto-improve only
touches `*.progress.html`; the CoS only touches `goals.html`/`org-pulse.html`. No two
writers ever share a file → no races even when everything runs at once.

## The card (assembled per workflow from its fragments)
```
Substack growth
🩺 Working   (Pulse: no bugs, last fix 3d ago)
💵 Cost ok   (Pulse report: $0.12 / 18k tokens · top spend reviewer)
🎯 Off-goal  (Auto-improve: open-rate flat · experiment "subject lines" running 2/5)
Trend ●●🔴🟢🟢                                          → click: improve.html (full history)
```
- **Status pills** per axis (set by the owning loop — *not* derived by the UI; severity
  like critical-vs-minor is a judgment only the loop has).
- **Rolling trend strip:** the card carries the last ~5–10 run statuses inline (overwrite
  the card each run, but keep the short strip) so you see trajectory without a history
  store. Full history stays in `improve.html` / `org-pulse.html`.

## Mechanism — how a loop updates the dashboard
Parallel to `notify_user`: a tool (e.g. **`update_org_dashboard`**) each loop calls with
its card fields. Important cadence difference from notify:
```
Pulse run → improve.html (detailed log)
          → update_org_dashboard(health + cost cards) ← ALWAYS (keeps dashboard live)
          → notify_user(email/whatsapp)          ← ONLY on a decision-worthy change
```
`notify_user` is sparing (don't spam); the **card updates every run** so the dashboard is
always current, not "last time something was notable." Auto-improve does the same for its
`progress` card.

## Layout
- **Triage bar** up top: `🔴2 · 🟠3 · 🟡1 · 🟢6 · ⚪1` — how's the org in one line.
- **Goal scorecard** (from `goals.html`) — are we winning.
- **Grouped by attention, not by goal.** A workflow's goal (from `success_criteria`) is
  almost always unique to that workflow, so grouping *by* goal just produces one-card
  "groups" — not a real grouping, and it buries what needs a look. Built as: **"Need
  attention"** (critical/bug health OR off-goal/at-risk progress OR elevated/missing cost)
  first, then
  **"Healthy / on-track"**. Each card still names its own goal as a short (3-6 word)
  distilled chip (`data-goal`, e.g. "Grow LinkedIn reach") next to the title — legible
  without being a grouping key.
- **Drill-downs:** `goals.html` (detailed scorecard) + `org-pulse.html` (journal) linked
  below — depth when wanted, out of the way otherwise.

## UI — a real dashboard, not embedded HTML
The Org page must render its **own** React components (status pills, goal groups, filters,
sort, triage bar) from the card **fields** — *not* embed the agents' raw HTML (that's the
doc-like, inconsistent surface we're replacing). `goals.html`/`org-pulse.html` remain as
embedded drill-down docs.

## Data carrier (decision: data-attributes; JSON noted as the alternative)
The React UI needs the card fields without a JSON data model. **Chosen: `data-*`
attributes on the HTML card fragments** — honors "no JSON" (the loops keep writing readable
HTML), and the app reads `data-status`/`data-goal`/`data-updated` plus `data-field="headline"`,
`data-field="metric"`, and `data-field="detail"` to render its components, group by attention
status, sort, and filter. `data-goal` is a
short distilled label (3-6 words), not the raw `success_criteria` text — Pulse
(`card.health.html` and `card.cost.html`, every run) and Auto-improve
(`card.progress.html`, on fire) write it,
so the goal chip shows up from the workflow's first run rather than waiting on the
less-frequent Auto-improve loop.
```html
<article class="pulse-card" data-workflow="substack" data-axis="health"
         data-status="healthy" data-goal="grow-subs" data-updated="2026-06-30T09:12Z">
  ...visible card content...
</article>
```
**Trade-off (documented):** the app parses HTML to extract fields (slightly hacky). If a
cleaner feed is preferred later, a tiny per-card JSON (`{status, verdict, goal, headline,
next}`) is the alternative — but that reintroduces the JSON we chose to avoid.

## Classification
Split across the three axes (loop-set, not UI-derived):
- 🩺 **healthy / bug / critical** (Pulse). critical = broken/blocking, act now; bug =
  fixable, not urgent.
- 💵 **normal / elevated / missing** (Pulse report step). elevated = spend/time outlier
  worth watching; missing = no reliable telemetry.
- 🎯 **on-track / at-risk / off-goal** (Auto-improve).
- `⚪ idle` when no recent runs/data.
The triage bar and filters key off these.

## What stays (drill-downs, unchanged)
- `improve.html` per workflow — the full per-workflow Pulse/improve history (click-through
  from a card).
- `org-pulse.html` — the CoS daily journal (the narrative/history at org level).
- `goals.html` — the detailed goal scorecard.

## Open / future
- Browsable per-card archive on the dashboard (vs the rolling trend strip + click-into
  `improve.html`) — deferred; the strip covers the common "is it improving" need.
- Whether Auto-improve's *own* health ("improvement loop stalled") is surfaced as a signal.
- Goal status is the CoS's daily judgment (a goal spans workflows); cards are live per-run.
  Accepted freshness split: live activity vs considered goal judgment.

## Implementation notes — corrected architecture (2026-06-30)

The "Writers & ownership" section above assumed ONE shared `pulse/cards/` dir. That is
**wrong**: workflows are **separate workspaces** (each has its own `workspacePath`;
`builder/improve.html` is per-workflow — see `auto_improvement_endpoints.go`). A
per-workflow loop cannot write into the org/CoS workspace's `pulse/`. Corrected, simpler
model (as wired):

- **Cards live in each workflow's OWN workspace, next to improve.html:**
  `builder/card.health.html` (Pulse loop) + `builder/card.cost.html` (Pulse report step) +
  `builder/card.progress.html` (Auto-improve loop). Perfect ownership, no shared dir, no
  cross-workspace writes.
- **No new tool.** The loops already have workspace write access through
  `diff_patch_workspace_file` / shell-backed writes. The card contract is inlined in the loop step prompts in
  `cmd/server/scheduler.go`: Pulse `postRunMonitorSteps()` STEP 8 (notify/final
  summary) writes `card.health.html` after triage, harden, artifact review,
  cost/time, backup, and publish are known; Pulse STEP 4 (LLM/cost/time report)
  writes `card.cost.html`; Auto-improve `wrapOptimizerImproveMessage()` STEP 2
  writes `card.progress.html`. The owning step OVERWRITES its card so the dashboard
  stays live.
- **Dashboard assembly (frontend, to build):** enumerate workflows via the existing
  `getWorkflowsOverview(workspacePaths[])` path, then per workflow read
  `getBuilderDoc(workspacePath, doc, filePath='builder/card.health.html')` +
  `'builder/card.progress.html'` + `'builder/card.cost.html'`, parse the `data-*`
  attributes, render the triage bar + attention-grouped cards. `pulse/goals.html` stays
  org-level (read as today).
- **Card contract** (single-quoted attrs — survives the Go string literal AND email):
  `<article class='pulse-card' data-axis='health|progress|cost' data-workflow='…'
  data-status='healthy|bug|critical (health) | on-track|at-risk|off-goal (progress) | normal|elevated|missing (cost)'
  data-goal='…' data-updated='ISO8601'><h4>name</h4><p data-field='headline'>…</p></article>`
  Health cards should also use named `data-field` rows when known:
  `metric`, `detail`, `state`, `input`, `fix`, `harden`, `artifact`, `backup`, `publish`,
  `cost`, `evidence`, and `next`. The org dashboard treats these as compact state,
  not the full email narrative. `input` summarizes open `Human input requested`
  cards in `builder/improve.html`, for example `0 open` or `1 open — approve cadence change`.
- **v1 scope:** current-status cards. Rolling trend deferred to v2.
