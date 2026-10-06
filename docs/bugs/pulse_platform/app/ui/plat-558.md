[← platform / frontend-chat](index.md)

# PLAT-558 — Panel help is manual; only product walkthroughs open automatically

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | app |
| Area | ui |
| Summary | fixed on main, not deployed. |

| Coordination | Value |
|---|---|
| State | fixed on main; not deployed |
| Date | 2026-10-06 |
| Owner | frontend-chat |

## Request and cause

The owner finds the automatic help popups on individual right-side panels
annoying. Keep automatic onboarding at the full product level; help inside
panels must be manual.

Commit `c8eef68a0` introduced shared contextual first-visit tips on 2026-09-30.
Each new help topic opened after a visibility delay, so exploring multiple
panels/subtabs interrupted the user repeatedly even though each topic appeared
only once.

## Done

- Panel help opens only from its question-mark button. Preserve its full guide,
  integration instructions, Ask AI, close control, Escape and outside-click
  behavior.
- Remove the shared automatic contextual-tip hook and Providers section hints,
  plus obsolete tip-specific tests and key generation. Providers keeps its
  manual help button and full walkthrough.
- Preserve existing full product walkthrough startup, readiness and dismissal
  rules in ModePresetBar and WorkflowWalkthrough.

## Verification

- 54 tests across panel help, full walkthroughs, Providers and Pulse passed.
  One focused regression checks visible new panel topics across Goals, Crew and
  Code stay closed beyond the old delay and after remount, while manual open and
  Escape still work.
- TypeScript and targeted lint passed.
- Source review confirms the automatic full product walkthrough branch and
  product tour implementation are unchanged. No deployed-instance check yet.

## Left

Release the frontend and check manual panel help on the owner's instance.

## Register notes

[PLAT-558](plat-558.md), P2, fixed on main, not deployed. Remove automatic contextual panel/section popups, keep manual help and existing product walkthrough startup. 54 frontend tests, TypeScript and lint passed.
