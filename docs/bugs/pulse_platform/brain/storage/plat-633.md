[← brain / storage](index.md)

# PLAT-633: Brain as a normal Git folder; backup through the terminal

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | brain |
| Area | storage |
| Summary | Brain is a plain Git folder like every other product: the Brain chat edits its files and runs git in a shell; Brain tools only store; brain_backup is removed |

## What happened

## Fix

## Left

## Decision

Owner, 2026-10-06: "agent should get raw access to file system and it should use git normally"; "we should not limit to a tool"; "the tool should just store stuff.. but git backup will work via terminal"; two-way, "like all other agents products". Why: the brain_backup tool wrapper wedged in BACKUP_OUTCOME_UNKNOWN on RTS (its git errors were hidden; every retry refused) and its commit arguments were rejected 8 times in a row.

## Plan

1. Brain picks up direct edits in its folder (new, changed, removed files; new directories) before every call. **Done.**
2. **Done.** Brain's folder moves into the documents tree as `Brain/` (copied from the state folder at startup, old copy kept), app-owned 0700, a Git working folder with its remote from Brain's backup settings and a credential helper reading `BRAIN_GITHUB_PAT`.
3. **Done.** The Brain chat gets a shell on `Brain/` (folder guard write grant) for admins and Owners of the whole Brain; changes are recorded as theirs.
4. **Done (surface).** `brain_backup`, the Brain tab's Git panel and its `/api/knowledgebase/git` route, and deletion reservations are removed; Brain tools only store. Left: delete the now-unreached backup internals in `pkg/knowledgebase` (backup.go receipts/push intents, git_workspace.go Files Git copies) and the backup status view.
5. A terminal on `Brain/` in the Brain tab.

## Details

- `Brain/` is admin-only in the Files proxy (`workspace_proxy_policy.go`); app-owned 0700; no folder guard includes it except the Brain chat of someone who is Owner of Brain's root (`brainShellGrant`), whose shell starts there.
- Git: `EnsureGitRepository` runs `git init` if needed, excludes `.kb-registry.json`, sets `origin` from the backup destination and a credential helper reading `BRAIN_GIT_TOKEN` (set in that shell's environment from the destination's `pat_secret`); commits are authored as the person. The workspace server passes `BRAIN_GIT_*` and git's author/committer variables.
- After each shell or file-edit command in that chat, Brain re-reads the folder (`SyncDisk`) as that person.
- Tests: `TestBrainPicksUpDirectEditsInItsFolder`, `TestBrainNotesMoveIntoDocumentsOnceAndStayAdminOnly`; backup-tool and reservation tests removed.
- Not verified live: needs an RTS deploy, then from the Brain chat `git status` / commit / push to the configured repository. The current push failure (`could not read Username`) should then show git's real message.

## RTS check 1 (2026-10-06)

Deployed 8ae73b047: notes moved into `/data/video-studio/docs/Brain` (13 files, 0700; old folder kept as `live.moved-…`). Two problems in the owner's first Brain chat turn:
- The shell ran as the owner's slot account (`slotctl: could not start … permission denied`): slot accounts cannot enter the app-only `Brain/`. Fixed: a command working in `Brain/` whose folder guard grants `Brain/` runs as the service account, still confined by Landlock (`isBrainFolderCommand`, test `TestOnlyGrantedBrainFolderCommandsRunAsTheService`).
- `git init` failed in the server with no message. The error now includes git's exec error, and Brain sets up the repository at service start and logs `[BRAIN] Git folder ready|not ready`.
