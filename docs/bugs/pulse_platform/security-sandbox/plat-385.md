[← Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-385 — On Linux, Full CLI's native writes ignored blocked paths (planning/, the raw database)

| Coordination | Value |
|---|---|
| State | stopgap on `main` (this commit); launcher enforcement open |
| Date | 2026-10-03 |
| Owner | security-sandbox |
| Related | `8b6e85d61` (Full CLI everywhere on Linux), PLAT-374 (launcher `hidden_paths` / `read_only_overlays`), found by ai-work-0b's review |

## Problem

Landlock grants whole folders and cannot carve blocked paths out of them.
Full CLI's native Bash/Write/Edit never pass the folder guard, so a workflow
chat on Linux could write `planning/plan.json` or `db/db.sqlite` directly where
the bridge would refuse. A Mac is not affected: Seatbelt denies blocked paths
inside granted folders.

## Stopgap (this commit)

A Linux chat with a blocked path inside one of its writable folders stays in
hybrid (native reads; shell and writes through the bridge, which enforces the
blocked paths). Chats without such a path (Code) keep Full CLI.
`blockedInsideWriteGrant`, `TestBlockedInsideWriteGrant`.

## Left (the real fix)

multi-llm-provider-go `clisandbox/landlock.go` sends `hidden_paths` (blocked
paths strictly inside a grant), `read_only_overlays` (blocked-write paths
strictly inside a write grant) and `private_tmp` to the launcher, refusing what
it cannot express. Needs: the launcher started in its user+mount namespaces for
tmux and structured launches and slot launches; an answer for Cursor's shared
`/tmp` sockets; pre-creating missing blocked files (the launcher skips ENOENT);
a Linux e2e (Excellence, with the owner's permission). Then remove the stopgap.
