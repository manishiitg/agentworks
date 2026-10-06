[← platform / security-sandbox](index.md)

# PLAT-374 — A blocked file sent agent shells to a weaker sandbox, as the service account

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P0 |
| Product | platform |
| Area | security-sandbox |
| Summary | **fixed** on `main` (`6f630cc25`), deploy pending: a blocked `db.sqlite` inside the project pushed Code agent shells off Landlock onto the mount-namespace fallback, which ran as the service account and could read the pl… |

| Coordination | Value |
|---|---|
| State | fixed on `main` (`6f630cc25`); Excellence, Confida and RTS deploy pending |
| Severity | P0 (server secrets readable by any Code agent shell) |
| Date | 2026-10-03 |
| Owner | security-sandbox |
| Related | PLAT-371 (blocked agent files), slot accounts (`docs/DECISIONS.md` 2026-10-01) |

## Problem

A Code agent installed nvm into the service account's home (`/srv/agents/home`),
not the user's project. Traced on Excellence: every Code agent shell call blocks
the project's `db/db.sqlite`, which sits inside the writable project folder.
Landlock cannot take access back from a subpath, so `landlockPolicy()` failed and
`ExecuteIsolated` silently fell back to the mount-namespace backend. That backend
ignores `Isolator.Slot` and runs the command as the service account.

Replayed as a slot user (read-only checks), the fallback could:

- read the platform `.env` (all server secrets);
- write the service account's home and the live release folder.

Other users' folders stayed hidden. The Code terminal was not affected (it sends
no blocked paths).

## Fix

- Blocked paths inside a granted path become `HiddenPaths`: the launcher mounts
  an empty, mode-000, read-only placeholder over each one in its own mount
  namespace (the same mechanism as read-only overlays for blocked-write paths).
- A policy Landlock cannot carry is refused (`SANDBOX_UNAVAILABLE`), never
  downgraded. A command that must run as a user's slot never uses the
  mount-namespace backend.
- Side effect, as the user asked: the agent shell's `HOME` is now
  `<project>/.sandbox-cache/home`, the same home as the Code terminal, so the two
  share installs and logins.

## Verified

On Excellence as the user's slot with the new launcher
(`TestShellWithABlockedFileRunsAsTheUsersSlotE2E`,
`TestBlockedFileInsideAWritableFolderIsHiddenNotAFallback`,
`TestBlockedPathContainingAGrantIsRefused`): runs as the slot, `db.sqlite`
unreadable and read-only, `.env` unreadable, service home not writable, project
writable.

## Left

- Deploy to Excellence, Confida and RTS (the launcher binary ships with the release).
- Secrets in `.env` were readable by Code agent shells until deployed; rotating them is the owner's call.
- Remove the stray nvm install and `.bashrc` / `.profile` lines from `/srv/agents/home` (awaiting OK).
- A missing blocked file (e.g. `db.sqlite-wal`) cannot be hidden and could be created.
- Pre-existing, not this change: `TestLandlockEnforcesExternalFolderAccess` fails on
  Excellence with the released launcher too (a blocked-write folder that is only a
  read path accepted a write). To investigate.

## Register notes

[PLAT-374](plat-374.md), P0, **fixed** on `main`
(`6f630cc25`), deploy pending: a blocked `db.sqlite` inside the project pushed
Code agent shells off Landlock onto the mount-namespace fallback, which ran as
the service account and could read the platform `.env`.
