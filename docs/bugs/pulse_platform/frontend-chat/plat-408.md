[← Platform issue index](../../pulse_platform_issue_register.md)

<<<<<<< HEAD
# PLAT-408 — Ops starts collapsed in the workflow header

| Coordination | Value |
|---|---|
| State | fixed on main; deployment pending |
| Date | 2026-10-04 |
| Owner | frontend-chat |

## User decision and implementation

Keep the Ops label and start its shared workflow/Relay toolbar group collapsed.
Clicking Ops expands or collapses its existing icons inline. Views and icon-only
Setup remain expanded. The shared group supplies the toggle and aria-expanded.
This supersedes the always-open Ops choice in PLAT-398.

## Verification

Updated the existing toolbar regression to check the initial collapsed state and
toggle wiring. Frontend TypeScript compilation and toolbar regressions passed.

## Remaining

Deploy the frontend through the normal release. The running apps were untouched.
=======
# PLAT-408 — Source Control listed the platform's `.sandbox-cache` folder, warned about it, and "Commit all" would have committed it

| Coordination | Value |
|---|---|
| State | fixed on `main`; deploy pending (Excellence and others) |
| Severity | P2 (junk, including a whole nvm install with its own `.git`, could be committed into a person's repository) |
| Date | 2026-10-04 |
| Owner | frontend-chat |
| Related | PLAT-403 (Code terminal), PLAT-404 (per-person home) |

## Problem

In a Code project's Source Control panel on Excellence the changes list showed `.sandbox-cache/` as untracked and a flood of
`warning: could not open directory '.sandbox-cache/cli-home/muse-cli/tmp/muse-workspace-probe-XXXX/': Permission denied`.
"Commit all 18" would have staged the folder.

## Cause

`.sandbox-cache/` is the platform's private folder inside the project (CLI home, tool installs, Muse's temp folders). It is owned by the person's
slot user, so the service account that runs the panel's git cannot open parts of it, and nothing told git to ignore it. In the user's own project it held
82 `muse-workspace-probe-*` folders (mode 700, owned by the slot user; the newest minutes old) and the nvm install.

## Fix

`workspaceGitCommand` (every panel git call: status, diff, stage, commit) now passes `core.excludesFile` pointing at a platform ignore list
(`.sandbox-cache/`). The folder is not listed, not walked (no permission warnings) and not added by "stage everything"; real changes still are.
Test: `TestWorkspaceGitIgnoresPlatformPrivateFolder` (fails without the fix).

## Left

- Deploy.
- The agent's and the terminal's own `git add .` are not covered: a repo's `.gitignore` or the person's global ignore still decides there.
- Muse leaves a new `muse-workspace-probe-*` folder on every start and never removes them (82 in one project, hundreds across Excellence): a Muse
  provider issue, sent to the session that owns it.
>>>>>>> 0113e13b1 (PLAT-408: Source Control ignores the platform's .sandbox-cache folder)
