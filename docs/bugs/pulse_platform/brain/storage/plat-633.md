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

## Readers see files only (2026-10-06)

Owner: Brain's folder and chat are for "only brain writers and owners"; Readers get "no chat, just files". `bootstrap` reports `can_write` (Editor or Owner of any folder; administrators always); the server refuses Brain chat turns from anyone else; the Brain tab shows a Reader only the Files view (their folders, parents as path only), with no chat and no Access/Models/Secrets views. Test `TestOnlyWritersAndOwnersCanWriteSomewhere`. Not deployed.

Next (agreed): a dedicated `brain` slot account owns `Brain/` (shared group with the service account) and runs Brain-folder commands; writers get the shell limited to their Editor/Owner folders; git for writers and Owners of the whole Brain.

## Changing the backup repository (2026-10-06)

RTS: the Brain chat could not point backup at `https://github.com/mprealtrainingsys/brain.git` (BACKUP_REMOTE_CHANGED, "changing the destination requires operator reconciliation"), a rule from the receipt-based backup. With Brain as a plain Git folder, an app-set destination can now be changed (the token secret is kept unless a new one is given), and the folder's `origin` follows at once. A destination set by the deployment's environment stays operator-only. Not deployed.

## Brain's own secrets (2026-10-07)

Owner: "in Brain we should not show all platform secrets, just its own"; "Vault = platform secrets and everything is product specific". Brain's secrets live in the shared encrypted secret store under `Brain` (like a Crew's or Code's own secrets). The Brain tab's Secrets view lists, adds/replaces and removes only these (names only; people who own the whole Brain). The Brain chat uses `brain_secrets` instead of the Vault-wide `list_secrets`/`manage_global_secret`. The backup token (`pat_secret`) is looked up in Brain's secrets; one that so far lived only among the platform secrets (RTS: `BRAIN_GITHUB_PAT`) is copied into Brain's store on first use. Test `TestBrainSecretsAreBrainsOwn`. Not deployed.

## Outage on RTS: every save refused after the move (2026-10-07)

RTS, 04:05 UTC: Brain returned 503 ("temporarily unavailable") for listing and search. Cause: Brain's mutation journal accepted only paths inside its data folder, but the notes now live in `Brain/` outside it. Every save (and every disk sync, "invalid internal journal path") was refused, and the refused journal stayed pending, so every later call failed in recovery. The tests kept the notes inside the data folder and missed it. Fixed: the journal accepts the notes folder too; regression test `TestBrainWorksWithItsFolderOutsideItsData`. Excellence was deployed with the bug at 06:20 server time (c15cf2229).

What happened in the folder: the owner's Brain chat reorganized RTS/Latency into RTS/Company, RTS/Engineering, RTS/Operations and RTS/Skills ("ok do optionA") by moving files in the shell; Brain could not record the moves. While diagnosing, the 13 original files were restored from the backup commit (db36150), so they now exist twice (old and new paths); removing the old copies was left to the owner.
