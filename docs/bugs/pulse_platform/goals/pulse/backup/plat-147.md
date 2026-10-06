[← goals / pulse / backup](index.md)

# PLAT-147 — the managed backup path can track its own Git bundle, causing every backup to recursively embed prior backups

| Field | Value |
|---|---|
| State | open |
| Priority | P0 |
| Product | goals |
| Area | pulse/backup |
| Summary | P0 — unbounded disk growth can make the machine unusable while |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `contained` — canonical path guard, future-agent Git guidance, bundle deletion, and verified history cleanup shipped; generalized managed external archive lifecycle/health remains separate product work |
| Last synchronized | `2026-08-19` |

- **Priority:** P0 — unbounded disk growth can make the machine unusable while
  the resulting backup becomes slow, stale, or impossible to verify.
- **Owner:** Pulse/finalizer backup contract, backup destination validation,
  workflow-builder guidance, and backup health reporting.
- **Observed on:** Tectonic USA Day Trading.

## Live evidence

- `backup/wf-backup.bundle` was approximately 67 GB before its explicitly
  authorized deletion on 2026-08-19.
- the workflow repository's `.git` directory is approximately 133 GB.
- `git ls-files` confirms the generated bundle is itself tracked.
- repository history repeatedly commits that bundle.
- the backup status reports bundle generation timing out and the portable
  bundle remaining stale.

This forms a self-amplifying graph: a bundle is committed into the repository,
then the next bundle includes repository history containing the previous bundle.

## Root cause

The backup contract relies on agent instructions but has no structural guard on
the destination. A generated backup may be placed and tracked inside the source
repository it snapshots. Git then treats the backup as ordinary source content,
and later backups recursively preserve it.

## Fix reasoning

Generated repository backups must live outside the repository being backed up,
for example under a platform-owned backup root keyed by workflow identity.

Before running a backup, the platform should:

1. canonicalize source and destination paths;
2. reject a destination inside the source repository or its `.git` directory;
3. reject a destination reported by `git ls-files`;
4. write to a temporary external path, validate `git bundle verify`, then
   atomically replace the previous good bundle;
5. report abnormal repository/bundle growth and retain the last verified backup
   on failure.

The tracked working-tree bundle has now been deleted. On 2026-08-19 the
repository history was rebuilt with only `backup/wf-backup.bundle` removed.
Before replacing the object store, the cleanup verified that every other path
and blob at `main` matched the original tree, ran strict object validation, and
preserved all six pre-existing working-tree modifications byte-for-byte. The
resulting repository is approximately 177 MiB instead of 133 GiB.

A complete, verified 166 MiB recovery bundle now lives outside the source
repository at
`/Users/mipl/ai-work/.agentworks-backups/tectonicusadaytrading/filtered-history-2026-08-19.bundle`.
Cleanup metadata records the old/new heads, tree comparison, status comparison,
and copies of the uncommitted files. The remaining product work is to make this
external, verified, atomic backup lifecycle and its health reporting a normal
platform capability rather than a one-off recovery procedure.

Future workflow agents now receive an explicit Git contract: the ordinary local
checkpoint is the commit itself; generated bundles/ZIPs/tarballs must never be
created in or staged from the source repository; and staged paths must be
inspected before commit. The shell bridge independently blocks relative,
ambiguous, symlink-resolved in-repository, and other in-source `git bundle
create` destinations. This prevents recurrence even if an agent ignores the
guidance.

## Acceptance

- Attempting to create a bundle under the source repository fails before write.
- A tracked destination fails with a clear actionable error.
- External temporary generation and atomic replacement preserve the last good
  bundle when verification or timeout fails.
- Repeated backups of an unchanged repository have bounded, stable size.
- Backup health exposes source size, bundle size, verification status, and
  timestamp without requiring an agent to inspect Git manually.
