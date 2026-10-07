[← chat / rendering](index.md)

# PLAT-674: Rare product tips while the agent works

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P3 |
| Product | chat |
| Area | rendering |
| Summary | A very rare, relevant product tip beside Working…: at most one a day, never for a feature already used |

## Why

Owner, 2026-10-07: use the "Working…" wait to teach product features, very rarely and without getting in the way, and
make it intelligent.

## What

- After the agent has been working for 10 seconds, the transcript's working row may show one muted line,
  "· Tip: …", fading in beside "Working…" (the row has a fixed height, so the list never moves). It goes when the
  turn ends.
- At most one tip a day per browser; each tip is shown once before any repeats; nothing is shown when no tip fits.
- Relevant: a tip names its products and an optional condition (for example Code with one tab: how to open another;
  with several: how to switch, and that tabs can ask each other). A tip for a feature the person already uses is never
  shown: ⌘K, ⌘J, hiding the toolbar and the Code tab keys are recorded when used (`utils/featureUsage.ts`).
- Tips live in one file, `frontend/src/products/productTips.ts`, and only name features that exist today.

## Verification

GitHub verify (`productTips.test.ts`, transcript activity tests, type check). Not seen in the app yet: after a restart,
a turn that works for more than 10 seconds may show a tip, at most once a day.

Follow-up 2026-10-07: the tip text is one size smaller than the footer (11px), at the owner's request.
