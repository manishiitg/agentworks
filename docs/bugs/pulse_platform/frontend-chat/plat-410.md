[← Platform issue index](../../pulse_platform_issue_register.md)

# PLAT-410 — Source Control listed the platform's `.sandbox-cache` folder, warned about it, and "Commit all" would have committed it

| Coordination | Value |
|---|---|
| State | fixed on `main` for the Files pane (`5d8672837`) and for sandbox git (this commit); deploy pending (Excellence and others) |
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

## Fix, part 2: git run inside the sandbox

Every sandbox home (the shell tool, the terminal, a person's slot home) sets `XDG_CONFIG_HOME=<home>/.config`, and git reads `$XDG_CONFIG_HOME/git/ignore` by
default. `EnsurePlatformGitIgnore` (workspace/security) puts `.sandbox-cache/` there (keeping a person's own list and adding the line once), called from
`privateSandboxHome`, `withHome` and `interactiveShellHome`. So the agent's `git add -A`, the terminal and the Files pane all skip the folder. In a Crew or workflow project that
folder is the project's HOME and holds git credentials, ssh keys and CLI logins; without this an agent could have committed and pushed them.
Tests: `TestEnsurePlatformGitIgnoreCreatesKeepsAndDoesNotDuplicate`, `TestSandboxHomeKeepsPlatformFolderOutOfGit` (real git with a `.git-credentials` file; fails without the fix).

## Left

- Deploy, then check on Excellence that a slot's home has `.config/git/ignore` (the service writes it best-effort; a home it cannot write keeps today's behaviour).
- A repo or person that sets `core.excludesFile` themselves overrides the sandbox list.
- Muse leaves a new `muse-workspace-probe-*` folder on every start and never removes them (82 in one project, hundreds across Excellence): a Muse
  provider issue, sent to the session that owns it.
- Other platform files still show as ordinary changes in the panel (`.dev-server.pid`, `AGENTS.md`, `product.json`, `workflow.json`, `planning/changelog/`).
