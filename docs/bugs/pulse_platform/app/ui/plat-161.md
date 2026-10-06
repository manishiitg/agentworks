[← platform / frontend-chat](index.md)

# PLAT-161 — the report-frame height fix (bd184c1f6) collapsed nested vh-sized iframes along with itself, making a real dashboard tab unreachable with nothing to scroll to

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | app |
| Area | ui |
| Summary | P1 — a real reporting dashboard tab was showing roughly 15% of |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `implemented` — verified against a real Chromium browser loading the actual affected report, not jsdom |
| Last synchronized | `2026-08-20` |

- **Priority:** P1 — a real reporting dashboard tab was showing roughly 15% of
  its actual content with no way to reach the rest; not slow, not confusing —
  invisible.
- **Owner:** `frontend/src/components/workflow/reportWidgets/HtmlWidgetFrame.tsx`
  (`resize`)
- **Related:** the regression this ticket fixes was introduced by
  `bd184c1f6` ("Report frames: inherit the bootstrap, surface errors, stop
  the height ratchet", 2026-08-18), which itself fixed a real, different bug
  (a tall report's frame never shrinking) and explicitly flagged that its own
  height change "is reasoned, NOT verified... jsdom has no layout engine...
  it needs a real browser check." It was right to flag that — this is the
  real-browser check, two days later, on a real report.

## How it surfaced

Reported live: the `salesoutreach` workflow's reporting dashboard could not
be scrolled at all.

## Root cause, verified against the actual report in a real browser

`resize()` (added in `bd184c1f6`) collapses the platform-managed iframe to
`0px` height, then measures `documentElement.scrollHeight`/`body.scrollHeight`
inside it, then restores/grows the frame to that measured value — collapsing
first specifically to defeat a real, different bug: those two properties can
never report less than the iframe's own current viewport height, so measuring
without collapsing first made a frame that was ever tall stay tall forever
(the original ratchet bug).

The collapse is not free: it changes the report's own viewport, and any
`vh`-sized content inside the report collapses along with it for the
duration of the measurement. `salesoutreach`'s reporting dashboard embeds a
*second*, nested `<iframe class="strategy-frame">` (its "GTM strategy" tab)
sized with `min-height: calc(100vh - 116px)`. `100vh` there resolves against
the platform iframe's own (momentarily 0px) viewport, so during every
measurement the nested iframe's height collapsed too.

Verified directly, not assumed: loaded the real `salesoutreach`
`db/reports/index.html` into a real Chromium instance (Playwright, a fresh
download — the project has no browser-test tooling today), reproduced the
platform's exact collapse-then-measure sequence, and switched to each tab:

| tab | measured height (shipped, buggy) | real content height |
|---|---|---|
| Lead pipeline (default) | 2519px | 2519px (correct — no nested iframe here) |
| Outreach | 463px | 464px (correct) |
| Email setup | 791px | 792px (correct) |
| **GTM strategy** | **319px** | **2052px** |

Isolated the nested iframe specifically: its own height measured **1884px**
with the platform frame at a normal 2000px, and **152px** during the
platform frame's momentary 0px collapse — the exact mechanism, confirmed on
the exact element.

## Fix

Replaced the collapse-then-`scrollHeight` measurement with one that never
touches the platform iframe's own height at all: take the maximum
`getBoundingClientRect().bottom` (plus any internal scroll offset) across
`document.body`'s direct children. An element's own laid-out position
reflects its real content regardless of the "root always fills the
viewport" guarantee that `scrollHeight` is subject to, so nothing needs to
be collapsed to sidestep it — which means nothing inside the report ever
sees an artificial viewport change, at any point.

Verified this also still fixes the *original* ratchet bug bd184c1f6 was
about, using the same real-browser harness: a synthetic report with
`body { min-height: 100vh }` (the shape that produces the ratchet) and a
panel that gets hidden after the frame was already tall. The new
measurement correctly detects the smaller size even while the frame is
still at its old, larger height — the collapse step was never actually
required to solve that problem, only to work around `scrollHeight`
specifically.

## Test coverage

None added to the repo. The height-computation logic is not meaningfully
testable in jsdom — `getBoundingClientRect()` returns 0 there just as
`scrollHeight` did, so a jsdom test would pass vacuously, which is exactly
the trap `bd184c1f6` explicitly declined to paper over with a fake green
test. Verified instead with a real, disposable Playwright + Chromium
harness against the actual report file and a synthetic ratchet
reproduction; not committed to the repo as permanent tooling — that's a
standing infrastructure decision (adding a browser-test category and a new
devDependency) deliberately left for a separate call rather than folded
into this fix.

Existing suite: `tsc -b` clean, full `vitest run` — 667 passing, one
pre-existing unrelated failure (`PulseWorkspace.test.tsx`, confirmed via
`git stash` to fail identically without this change) — no regressions.

## Acceptance

- The `salesoutreach` reporting dashboard's GTM-strategy tab is fully
  scrollable/reachable.
- The other three tabs, and the default view, are unaffected (confirmed:
  heights match within 1px, the platform frame's own border/rounding).
- A report that legitimately needs to shrink after loading still shrinks
  (the original `bd184c1f6` fix's own acceptance bar).
