[← Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-446 — Crew CLI turns run as the app account, not the owner's slot (decision 1 of PLAT-442 rests on a wrong premise)

| Coordination | Value |
|---|---|
| State | decided 2026-10-04: keep the app account; nothing to build (found while doing PLAT-442 step 2) |
| Date | 2026-10-04 |
| Owner | security-sandbox |

## Finding

PLAT-442 decision 1 says a Crew Run-mode reader's turn runs as the Crew owner's slot, because it runs "in the owner's folder" and the folder
names the owner. The CLI of a Crew turn does not start in the Crew folder. `crewCLIWorkingDir` (always on for coding CLIs) starts it in an
isolated runtime folder under the app's state root (`<AGENTWORKS_STATE_ROOT>/cli-runtimes/v1/<hash>`, app-owned, mode 0700) that links to the
project. That folder names no user, so the old path rule (`slotfs.SlotOf`, `slots.SlotForDir`) gave no slot: Crew turns of the owner AND of a
reader both run as the app account, with Landlock and the reader block, tools and folder guards as the limits. Goal chats are the same
(isolated runtime). Only Code, private chats and delegated sub-agents (their folders are in the user's tree) run as the user's slot.

Pinned by `cmd/server/multiuser_identity_test.go` `TestRunAsRegressionTable` (the Crew rows) and `identityExpectation` in
`multiuser_fixture_test.go`, which is where the expected slot of each turn kind is stated.

## Why it matters

- The premise of the Crew move (step 4): moving a Crew to `Crew/<id>` cannot take a CLI isolation away that Crew turns never had. It can only
  matter for the bridge shell tool, which runs as the CALLER's slot (`slots.For(X-User-ID)`), so a reader's shell commands run as the reader's
  slot in the owner's folder, not as the owner's.
- If the owner wants Crew turns to run as the owner's slot (own logins, own home, Docker), that is new behaviour: the runtime folder would have
  to live where the slot can use it (the slot's state area), the provider launch files and tmux session would follow, and the Crew reader's
  turn would use the owner's slot while the shell tool uses the reader's, which must then be settled too.

## Decision (owner, 2026-10-04)

Keep the app account for Crew CLI turns, as today. The limits on a Crew reader stay the platform's tools, folder guards and the reader block, with Landlock underneath.
Decision 1 of PLAT-442 is corrected accordingly (DECISIONS.md, plat-442.md). Making Crew turns run as the owner's slot stays a possible later change; it is not planned.

## Left

Nothing. `decideTurnRunAs` and the fixture's `todayIdentity` stay the two places that state who runs as whom.
