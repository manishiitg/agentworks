[← brain / backup](index.md)

# PLAT-619: Deleted paths locked while backup has never run

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | brain |
| Area | backup |
| Summary | Deleted Brain paths stayed locked forever when backup was configured but had never succeeded |

## What happened

## Fix

## Left

## What happened

RTS, 2026-10-06: an agent tidying Brain could not create notes at their topic paths: "the natural topic-folder filenames are locked until Brain Git backup confirms earlier deletions, and that backup is unavailable". Backup status: configured (the remote was saved by the setup card), never a successful backup, "Git backup is unavailable", six deletions pending. A deleted path is reserved until its deletion is backed up, so a restore cannot collide; the live-only exception released it only when no remote was configured at all.

## Fix

`deletionReusable` releases a deleted path whenever nothing was ever published (backup never initialized, no tip, no published paths, no receipts), whether or not a remote is configured. After the first real backup the strict rule applies unchanged. Pinned by `TestNeverPublishedBackupReleasesDeletedPath`; the published-deletion and corrupt-state tests still refuse.

## Left

Deploy RTS. Separately, RTS backup still needs its token (Brain → Secrets) so backups run at all.
