[← app / activity](index.md)

# PLAT-410 — Source Control listed the platform's `.sandbox-cache` folder, warned about it, and "Commit all" would have committed it

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | app |
| Area | activity |
| Summary | fixed on `main`; deploy pending. |

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

Two routes, because a person's slot home (`/srv/<app>/slots/home/<slot>`) is owner-only and the service cannot write into it:

- A project's own home (Crew, workflow, a terminal that is not a slot): every such home sets `XDG_CONFIG_HOME=<home>/.config`, and git reads `$XDG_CONFIG_HOME/git/ignore`
  by default. `EnsurePlatformGitIgnore` puts `.sandbox-cache/` there (a person's own list is kept, the line added once).
- A user's slot (Code on Excellence): `withPlatformGitIgnoreEnv` writes `<project>/.sandbox-cache/git-ignore` (the service writes it, the slot reads it) and sets
  `GIT_CONFIG_COUNT/KEY/VALUE` to `core.excludesFile`, extending any existing `GIT_CONFIG_COUNT`. Nothing is created when the project has no `.sandbox-cache`.

In a Crew or workflow project that folder is the project's HOME and holds git credentials, ssh keys and CLI logins; without this an agent could have committed and pushed them.
Tests: `TestEnsurePlatformGitIgnoreCreatesKeepsAndDoesNotDuplicate`, `TestSandboxHomeKeepsPlatformFolderOutOfGit`, `TestSlotHomeEnvPointsGitAtTheProjectsIgnoreList` (real git; fail without the fix).
Found on Excellence (release `agents-02f47720`): the first version only wrote the XDG file, which the service cannot do in a slot home; `git add -A -n` in the owner's project still listed 974
`.sandbox-cache` paths. The slot route above is the fix for that.

## Left

- Deploy, then check on Excellence that a slot run no longer lists `.sandbox-cache` in `git status` / `git add -A -n`.
- A repo or person that sets `core.excludesFile` themselves overrides the sandbox list.
- Muse leaves a new `muse-workspace-probe-*` folder on every start and never removes them (82 in one project, hundreds across Excellence): a Muse
  provider issue, sent to the session that owns it.
- Other platform files still show as ordinary changes in the panel (`.dev-server.pid`, `AGENTS.md`, `product.json`, `workflow.json`, `planning/changelog/`).

## Register notes

[PLAT-410](plat-410.md), P2, fixed on `main`; deploy pending. Panel git and sandbox git (agent, terminal) ignore the platform folder.
