# PLAT-517 — Consolidate browser design into one guide

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P3 |
| Product | platform |
| Area | browser |
| Summary | fixed on main. |

State: fixed on main. Date: 2026-10-05. Priority: P3. Requested by owner.

## Problem

Browser behavior is split between the main guide, the extension design and its
README. The main guide still describes five-minute pairing and broader extension
product availability, conflicting with the implemented Code-only stable-code policy.

## Completed

- Merge the full extension design, installation and deployment instructions into
  [the browser guide](../../../../core/browser.md).
- Document stable account/project codes, Code-only scope, replacement/reset,
  branded groups, opt-in focus, per-tab diagnostics and durable chat notices.
- Clarify the differences between workspace, direct-CDP and extension methods;
  distinguish extension limits from direct-CDP file/recording/concurrency behavior.
- Add a reading map and extension source links. Point active references to the
  canonical guide; retain the former design path as a compatibility link.
- Reduce the extension README to source location and links to the same guide.

## Verification

Reviewed the merged content against PLAT-513/516 and the prior design/README.
Checked local Markdown link destinations and anchors in changed documents,
reviewed the diff and ran git diff --check. Documentation only; no runtime tests
or new deployment qualification are needed or claimed.

## Remaining

None for documentation consolidation. Runtime rollout status remains in the
original implementation tickets; this change does not deploy the browser feature.

## Register notes

[PLAT-517](plat-517.md), P3, fixed on main. Merge extension design/setup/deployment into the canonical browser guide; correct stable-code and Code-only policy, preserve old links.
