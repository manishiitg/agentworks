[← brain / storage](index.md)

# PLAT-633: Brain as a normal Git folder; backup through the terminal

| Field | Value |
|---|---|
| State | open |
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
2. Brain's folder moves into the documents tree as `Brain/` (copied from the state folder at startup, old copy kept), app-owned 0700, a Git working folder with its remote from Brain's backup settings and a credential helper reading `BRAIN_GITHUB_PAT`.
3. The Brain chat gets a shell on `Brain/` (folder guard write grant) for admins and Owners of the whole Brain; changes are recorded as theirs.
4. `brain_backup` (status/commit/push/git) and its push/receipt state and deletion reservations are removed; Brain tools only store.
5. A terminal on `Brain/` in the Brain tab.
