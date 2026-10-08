[← coding-agents / accounts](index.md)

# PLAT-715: Only admins share provider accounts widely

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | coding-agents |
| Area | accounts |
| Summary | A member may share their own provider account only with up to 10 named colleagues; sharing with workflows or Crews (which reach everyone who runs them) is admin-only, enforced on save and at use. |

## What happened

A member could add a personal provider account and share it with any
workflows, Crews and up to 200 people. A workflow share reaches everyone who
can run the workflow (readers, schedules, bots) and a Crew share every Crew
user, so a member could in effect put their account behind everyone's turns.
Owner decision (2026-10-08): only admins make an account usable that widely;
members may still share with named colleagues.

## Fix

- `normalizeProviderSharing` (create and update): for an owner who is not an
  admin, workflows/Crews are refused ("Only an admin can share a provider
  account with workflows or Crews; you can share it with up to 10 named
  colleagues.") and more than 10 people is refused. Admins keep the 200 cap.
- Use time: `effectiveProviderSharing` keeps, for an owner who is not
  currently an admin, only the first 10 named people and drops workflow and
  Crew entries. `userAccountAdmission` uses it, and it is the only path for
  "shared with you / workflow / Crew" (turn admission, credential
  resolution, the provider connections list and every picker fed by it).
  Existing member shares with people keep working; their workflow/Crew
  entries stop counting without a data migration, and an owner who loses
  admin stops sharing widely at once. The owner's own view shows the
  effective sharing.
- Frontend: a member's "Who can use it" (and the add-account form) offers
  only "Shared with named colleagues (up to 10)" with the people picker.
- Tests: `TestProviderAccountsSharedWithWorkflow` (member refused for a
  workflow and for 11 people, member shares with a named colleague who can
  use it, an existing workflow share stops admitting once its owner is not an
  admin, admin can share); other sharing tests make the owner an admin; one
  vitest for the member picker.

## Left

- Not verified live on a server.

