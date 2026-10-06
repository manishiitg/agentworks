[← relays / frontend-chat](index.md)

# PLAT-373 — Relays product introduction

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | relays |
| Area | frontend-chat |
| Summary | implemented and locally verified; deployment pending. |

| Coordination | Value |
|---|---|
| State | implemented and verified locally; deployment pending |
| Date | 2026-10-03 |
| Owner | frontend-chat |

## Request

Replace the sparse “Select a Relay” empty page with an introduction like the
other products, with high reuse of their existing layout and creation flow.

## Implemented

- `ProductIntro` is shared by Crew, Code and Relays; existing Crew/Code copy,
  actions and walkthrough targets are retained.
- Relays explains building graphs in chat, testing draft inputs/outputs and
  publishing an API version while continuing to edit the draft.
- “Create your first relay” opens the existing preset dialog through
  `useCommandDialogStore`. The same account permission guards both this action
  and the top-bar plus button. Accounts without create access see guidance to
  select an existing relay.
- The creation form's title, placeholder and help text now use Relay naming.
- This adds the introduction page; the Relays guided walkthrough is still disabled.

## Verification / remaining

- TypeScript check and Vite production build passed.
- 11 targeted intro/top-bar tests passed, including disabled creation and the
  shared dialog request; 8 existing Crew creation tests passed with the local
  Node storage file enabled (the default runner lacks localStorage).
- In-app browser at an isolated port rendered the actual intro and top bar;
  clicking the CTA opened the existing Relay creation form. No backend was
  started or changed for this UI check.
- Remaining: deploy the source change to Excellence to show it there.

## Register notes

[PLAT-373](plat-373.md), implemented and locally
verified; deployment pending. Relays shares Crew and Code's intro layout and
opens its existing creation dialog with the account create permission.
