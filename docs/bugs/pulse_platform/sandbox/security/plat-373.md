[← platform / security-sandbox](index.md)

# PLAT-373 — Every shell command failed with "Invalid folder guard write path" on a local machine

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P0 |
| Product | sandbox |
| Area | security |
| Summary | **fixed** on `main`: the folder-guard boundary check refused absolute host grants and the old `Downloads/` link, so every local shell command returned HTTP 400. |

| Coordination | Value |
|---|---|
| State | fixed on `main` (`1ebd1aba9`, this commit); local verification pending |
| Date | 2026-10-03 |
| Owner | security-sandbox |
| Related | the 2026-09-30 folder-guard boundary check (`6b70472f8`) |

## Problem

After the boundary check landed, every `execute_shell_command` from a local
Pulse, Builder, scheduled run or Code chat returned HTTP 400 "Invalid folder
guard write path", even `pwd`. Pulses and decision drains could do nothing.

## Causes

1. Absolute host grants outside the workspace (`/Users/mipl/Downloads`, a
   granted project folder) were refused. Fixed in `1ebd1aba9`: an existing
   outside directory passes and is never created.
2. Every folder guard granted the old workspace `Downloads/`. Locally it is a
   link to `_users/default/Downloads`, which the check refuses (a link may not
   carry a path into a user's folder from outside it). The folder is a
   leftover; fixed by no longer granting it anywhere and pointing the prompts
   at the chat or workflow folder instead.

## Done / left

- Done: grants removed from chat, workflow, delegation, tool and profile
  folder guards; prompts updated; tests (`shell_guard_writepath_test.go`,
  `agent_profile_sandbox_test.go`). The real failing session's other write
  paths pass the check.
- Left: confirm a local Pulse runs shell commands; the 113 files in
  `_users/default/Downloads` and the `workspace-docs/Downloads` link are left
  for the owner to delete; the per-run `execution/Downloads` and the host
  `~/Downloads` (CDP browser) are unchanged.

## Register notes

[PLAT-373](plat-373.md), P0, **fixed** on `main`:
the folder-guard boundary check refused absolute host grants and the old
`Downloads/` link, so every local shell command returned HTTP 400.
