[← sandbox / confinement](index.md)

# PLAT-383 — Absolute host grants reached server sandboxes with no root check

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | sandbox |
| Area | confinement |
| Summary | **fixed** on `main`: workflow `folder_access` had no assigned-roots check and absolute grants reached server CLI and shell sandboxes; now root-checked on update and kept only on a person's own Mac. |

| Coordination | Value |
|---|---|
| State | fixed on `main` (this commit); not deployed |
| Date | 2026-10-03 |
| Owner | security-sandbox |
| Related | PLAT-373 (`1ebd1aba9`), `8b6e85d61`; found by ai-work-0b's code review |

## Problem

`1ebd1aba9` let the shell accept any existing absolute folder outside the
workspace, and `cliPolicyPath` kept absolute grants in the CLI sandbox policy.
On a server that turned a workflow's `folder_access` into a real grant, and
`folder_access` only needed workflow ownership (no admin-assigned roots check,
unlike Work folders). A member could grant their agents the service account's
config or another user's home. `CDPHostDownloadsPath` also granted the service
account's `~/Downloads` on any server where CDP defaulted on.

## Fix

- Manifest update: a new or changed `folder_access` path must be inside the
  caller's admin-assigned roots (admins exempt; unchanged grants kept), the
  same check as adding a Work folder (`checkWorkflowFolderGrants`).
- CLI sandbox policy: absolute grants are kept only on a person's own Mac and
  dropped on every server (`cliPolicyPath`).
- Shell: the outside-folder exception applies only on a person's own Mac in
  native mode (macOS, NATIVE_WORKSPACE, not multi-user, no slots); servers
  refuse outside grants as before. The terminal's unconfined rule also now
  requires not multi-user.
- Host Downloads: granted only in local single-user CDP mode, or when a
  deployment names the folder explicitly.

## Done / left

- Done: `workflow_folder_grant_roots_test.go`, `TestCLISandboxPolicyDropsHostGrantsOnServers`,
  `TestIsExistingHostGrant`, `TestInteractiveShellUnconfinedNeverOnAMultiUserMac`,
  `cdp_host_downloads_test.go`.
- Left: on servers, legitimately assigned Work folders are not in native CLI
  sandboxes (they were not before either); allow them from the assigned roots
  when needed. A workflow.json edited outside the API is not root-checked.

## Register notes

[PLAT-383](plat-383.md), P1, **fixed** on `main`:
workflow `folder_access` had no assigned-roots check and absolute grants reached
server CLI and shell sandboxes; now root-checked on update and kept only on a
person's own Mac.
