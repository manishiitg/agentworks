[← platform / security-sandbox](index.md)

# PLAT-385 — On Linux, Full CLI's native writes ignored blocked paths (planning/, the raw database)

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | sandbox |
| Area | security |
| Summary | **fixed** on `main`: Landlock grants are split around blocked paths, so native tools cannot read or write planning/ or the raw database; tested under the real launcher. |

| Coordination | Value |
|---|---|
| State | fixed on `main` (multi-llm-provider-go `2c535cf`, builder stopgap removed); not deployed |
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

## Fix

multi-llm-provider-go `2c535cf` (`clisandbox/landlock_blocked.go`): when a
grant contains a blocked path, the grant is split. The containing folder is no
longer granted as a whole; each entry is granted on its own down the path to
the blocked one, which is left out. A read-blocked path is cut out of read
grants too. No mounts or namespaces (the launcher's `hidden_paths` /
`read_only_overlays` need user namespaces the agent server is not allowed to
create, and Cursor needs the shared /tmp), so every launch path is covered.
The PLAT-385 stopgap (hybrid for such chats) is removed.

The CLI's own working folder is never split (provider `71ca588`): its blocked
entries are the CLI's managed instruction files (CLAUDE.md, AGENTS.md,
.mcp.json, ...), which stay bridge-guarded; splitting it would have stopped a
Code CLI creating files in its project (found by ai-work-0b).

Accepted limits (owner-facing): no new file or folder can be created directly
in a split folder during that launch, for example the real workflow root next
to planning/ or AGENTS.md (workflow chats run in their own folder and write
outputs under runs/, code/ and so on, which stay writable); entries created
after the launch are not writable natively until the next launch.

## Done / left

- Done: `TestSplitAroundBlocked`; `TestBlockedPathsUnderTheRealLauncher` run
  on Excellence against the installed launcher (planning/ readable not
  writable, db.sqlite neither, a new db.sqlite-wal refused, the rest of the
  workflow writable); the whole clisandbox package passes there.
- Left: deploy; a live chat check on RTS after deploy.

## Register notes

[PLAT-385](plat-385.md), P1, **fixed** on `main`:
Landlock grants are split around blocked paths, so native tools cannot read or
write planning/ or the raw database; tested under the real launcher.
