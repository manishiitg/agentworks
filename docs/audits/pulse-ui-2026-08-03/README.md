# Pulse popup UI audit — 2026-08-03

## Scope

Combined UX and screenshot-based accessibility audit of the server A Latency Pulse
popup. The user goal is to understand what Pulse is handling, what needs a
workflow run, what needs the user's decision, and where to inspect reviewer
evidence without reading an internal database console.

## Steps and health

1. **Open pending decisions — needs improvement.** The decision panel is
   readable, but the same urgency was repeated in a large lifecycle warning at
   the top of the Pulse popup. Evidence: `01-needs-your-decision.png` and
   `02-pulse-overview.png`.
2. **Understand current Pulse work — needs improvement.** “Action required” did
   not say whether the user or Pulse owned the action. Migration counts and raw
   issue/review totals displaced the useful ownership summary. Evidence:
   `02-pulse-overview.png`.
3. **Inspect an issue — mixed.** The database contains useful problem, fix,
   verification, and history evidence, but the lower reviewer inspector rendered
   a second, more technical issue tracker with lifecycle terminology and IDs.
   Evidence: `03-issue-detail.png`.
4. **Review the updated overview — healthy.** The duplicate alert is removed;
   the top summary now separates Pulse work, user decisions, and checks waiting
   on a workflow run. Empty goal-impact evidence is compact and explains the
   missing producing run. Evidence: `05-updated-overview-top.png`.
5. **Inspect reviewer evidence — healthy.** Reviewer cards now lead to one latest
   judgment and an optional forensic report. Findings, fixes, verification, and
   activity remain in the single Issues tracker instead of being rendered again.
   Evidence: `08-final-reviewer-layout.png`.

## Changes made

- Removed the duplicate loop-closure warning from the Pulse modal.
- Reworded queues around ownership: **Pulse to fix**, **Waiting on run**,
  **Your decisions**, and **Platform team**.
- Replaced migration/debug metadata in the main health card with a plain owner
  summary.
- Renamed **Issue lifecycle** to **Issues** and clarified the filter descriptions.
- Reduced reviewer details to **Judgment** and **Full report**, removing the
  duplicate findings/activity UI and its extra API fetch.
- Made the reviewer grid three columns for the three current reviewers.

## Accessibility notes and limits

The updated queues and reviewer views remain real buttons/tabs with accessible
selected state. Ownership no longer depends on color alone because each state
has a text label. This screenshot audit does not prove keyboard order, focus
visibility throughout the long modal, screen-reader announcements after async
refresh, contrast ratios, zoom reflow, or mobile behavior; those require
interaction and automated accessibility checks.
