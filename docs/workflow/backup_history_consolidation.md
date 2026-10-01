# Backup, History & Versions Consolidation

Status: **Phase 1 + 2 + 3 implemented** (2026-06-21); Phase 4 (per-commit
history↔restore linkage + auto-snapshot) deferred — needs a data model that does
not exist yet (changelog entries are not tagged with backup commit refs, and there
are no builder hooks for "risky bulk op").

> 2026-09-30: the ZIP workspace export/import endpoints and the `local_zip`
> strategy were removed (see `docs/DECISIONS.md`). References to them below
> describe the pre-removal design.

Implemented so far:
- **Phase 1 — Versions removed.** Deleted `WorkflowVersionsPopup.tsx`, the toolbar
  Versions button + state, the 4 version API methods, the `WorkflowVersionMeta` type,
  the 4 backend handlers + routes (`workflow.go` version block + `server.go` routes),
  and the `local_versions` backup strategy. Shared test helpers that lived in
  `workflow_versions_test.go` were preserved in new `workspace_mock_test.go`.
- **Phase 2 — Restore from Backup + git-default tiering.** `handleRunWorkflowBackup`
  now accepts `action:"restore"` with an optional `target_ref`, backed by a new
  agent-driven `buildWorkflowRestoreAgentPrompt` (git checkout into the workspace, no
  remote rewrite). Backup popup has a **Restore** button + confirmation (restores latest;
  commit picker deferred to Phase 4). Git is marked the default primary strategy and the
  configure prompt defaults to a zero-config local git repo, nudging a GitHub remote for
  off-box durability.
- **Phase 3 — History view.** The "Pulse" tab is renamed **History** and is now a
  two-lens surface: **Timeline** (the existing `improve.html` narrative via `LogViewer`)
  and **Plan edits** (new). Plan edits is backed by a new
  `GET /api/workflow/plan-changelog` endpoint (`workflow_changelog.go`) that merges
  `planning/changelog/*.json` into a newest-first feed of tool · reason · per-field
  old→new diffs, rendered by `PlanChangelogFeed.tsx` inside `HistoryView.tsx`.

## Problem

We accumulated three overlapping features for "don't lose / understand my workflow":

1. **Backup** — AI-driven, multi-destination, off-box durability. Now the *primary* strategy.
2. **Versions** — local numbered snapshots (`versions/v{N}/`), manual rollback points.
3. **Changelog / History** — the narrative of *what changed and why*.

These were built at different times and now confuse each other. Backup is clearly
primary. Versions no longer earns its own top-level slot. History is the most
durably valuable artifact but is under-framed. This doc defines the target: **two
first-class citizens — Backup (durability + restore) and History (what & why) —
and the removal of Versions as a standalone feature.**

## Current state (facts)

### Backup (keep — primary)
- Config in `workflow.json` `.backup`; operational status in `backup/status.json`.
- Strategies (`workflow_backup.go:82`): `git`/GitHub, `object_store` (R2/S3/B2),
  `huggingface`, `local_zip`, **`local_versions`** (← this *is* the versions feature,
  already modeled as a backup strategy type).
- Agent-driven (`handleRunWorkflowBackup`, `workflow_backup.go:343`). States:
  `not_configured`, `configured_not_verified`, `running`, `healthy`, `stale`,
  `partial`, `failed`.
- Tracked for staleness (`workflow_backup.go:161,172`): `workflow.json`,
  `planning/*.json`, `reports/report_plan.json`, `variables/variables.json`,
  **+ `knowledgebase/` and `learnings/` folders**.
- UI: dedicated **Backup popup** + toolbar button with status dot (added 2026-06-19).

### Versions (remove)
- Endpoints `workflow.go:4524/4674/4804/4915` (publish/list/revert/delete).
- On disk: `versions/v{N}/version_meta.json` + snapshotted files.
- Covers a **strict subset** of backup: config files + `learnings/`, but **not**
  `knowledgebase/`.
- **Manual-publish only** — no auto-snapshot before AI edits, revert, or harden/improve.
- **Effectively unused**: e.g. the `linkedin` workflow has exactly one version,
  published once in April 2026. Nobody remembers to click Publish.
- Why it's redundant: a git backup destination already produces a **durable, off-box,
  automatic commit timeline** — a strictly better version history than local snapshots.

### History / Changelog (elevate)
Two distinct logs, both kept:
- **`builder/improve.html`** — the high-level human-facing narrative (applied/proposed
  changes, review findings, monitor notes, user rules). **Already surfaced** in the UI
  as the **"Pulse" tab** (`WorkflowCanvas.tsx:307` → `LogViewer.tsx`, via
  `getBuilderDoc('improve')`, endpoint `auto_improvement_endpoints.go:145`). Archives via
  `getBuilderDocArchives`.
- **`planning/changelog/*.json`** — automatic, per-plan-mod-tool-call audit trail with
  mandatory `reason` and per-field old→new diffs (`planning_agent.go:964`). **No UI today.**

## Target model

```
Backup   →  "Can I get it back?"   durability + restore.  Multi-destination, AI-driven.
History  →  "What changed & why?"  narrative + plan-edit audit.  Read-only timeline.
(Versions: deleted — its job is absorbed by git-backup history + Backup's restore.)
```

Two toolbar citizens instead of three. The standalone **Versions** button and popup
are removed.

## Decision 1 — Kill Versions

### Remove
- Frontend: `WorkflowVersionsPopup.tsx`, its toolbar button + state in `WorkflowToolbar.tsx`,
  the `listVersions/publishVersion/revertToVersion/deleteVersion` API methods.
- Backend: the four version handlers (`workflow.go:4507–4994`) and their routes.
- Drop `local_versions` from the supported-strategies list (`workflow_backup.go:82`).

### Replace rollback with **Restore from Backup** (critical — this is what versions did)
Killing versions removes the only one-click rollback. Restore must move to Backup,
consistent with the agent-driven model:
- New action in the Backup popup: **"Restore from backup"**.
- Source = a configured durable destination (git commit, or object-store snapshot).
- For git: list recent commits on the backup branch; selecting one runs a builder
  "restore" task that checks out that commit's tracked files back into the workspace.
- Implemented the same way as `handleRunWorkflowBackup` — an agent task with a
  `restore` action and a target ref, writing progress to `backup/status.json`.

### Migration of existing `versions/` data
- **Do not auto-delete** existing `versions/v{N}/` folders — harmless on disk.
- Stop writing new ones; hide all UI.
- One-time: a small notice in the Backup popup if `versions/` exists ("Local snapshots
  are deprecated — set up a git backup for durable history"). No automatic conversion.

### Risk / requirement
Killing versions makes **restore depend on a configured durable destination**. A
workflow with no backup configured has no rollback. Mitigations:
- Backup popup nudges setup when `not_configured`.
- Keep **`local_zip` export** as the offline safety net (already in the Backup popup).
- Optional follow-up: a lightweight auto-commit to a local git repo under the workspace
  as a zero-config default destination (gives restore without remote setup). *Deferred.*

## Decision 2 — Dedicated History view

Most of this exists as **Pulse**. The work is reframing + adding plan-level detail +
a restore hook, not building from zero.

### Shape
A History surface (extend the existing Pulse tab, or a sibling tab/popup) with:
1. **Timeline** (default) — render `builder/improve.html` (reuse `LogViewer`). This is
   the authored "what & why" narrative. Newest-on-top, with archive months from
   `getBuilderDocArchives`.
2. **Plan edits** (new) — parse `planning/changelog/*.json` into a granular feed:
   timestamp · tool · `reason` · affected step ids · per-field old→new diffs. Needs a
   new read endpoint (or reuse the workspace file API) to list + read changelog files.
3. **Restore hook** — where an entry corresponds to a backup commit, offer
   "Restore to this point" → triggers the Backup restore flow above. (Linking entries to
   commits is **phase 2**; phase 1 ships the read-only timeline.)

### Naming
Keep the user-facing name **"Pulse"** (already established) or rename to **"History"** —
to be decided at implementation. Either way it is the single "what changed & why" window.

## Decision 3 — Git is the default primary provider (tiered model)

Backup gets an opinionated, tiered default instead of a flat list of equal strategies.
Everyone uses git, so git is the baseline; heavier content escalates to bulk storage.
Two axes: **git is the timeline** (local default → GitHub upgrade); **object_store and
HuggingFace are interchangeable bulk stores** for content that shouldn't live in git.

| Tier | Strategy | Holds | Rationale |
|------|----------|-------|-----------|
| **Normal default** | `git` (local) | workflow.json, planning, knowledgebase, learnings, scripts | text + small JSON, diffable; this is the rollback timeline, zero config |
| **Primary upgrade** | **GitHub** (git + remote) | same as above, off-box | durable, off-box copy of the timeline; the recommended upgrade |
| **Tertiary (bulk storage)** | `object_store` (R2/S3/B2) **+** `huggingface` | run folders, media, large artifacts, datasets | both are storage backends; HF Hub also offers storage/LFS. Agent picks per content type |

### Local git vs GitHub (the key nuance)
The `git` strategy is currently labeled "Git / GitHub" (`workflow_backup.go:85`) and
conflates two things:
- **Local git** (`.git` in the workspace, auto-commit) — zero config, instant rollback,
  but **on-box** (not a real off-box backup; it's a better Versions).
- **GitHub** (git + remote) — off-box durability, needs auth (token/SSH).

**Default = local-git auto-commit.** Every workflow gets a real commit timeline + restore
the moment it's created, no setup. The Backup popup then nudges "add a GitHub remote for
off-box durability." This is also the clean answer to the Decision 1 risk: with local-git
as the default, killing Versions never leaves a workflow without rollback.

### Agent picks the secondary automatically
The builder agent escalates to `object_store` only when the workflow actually produces
large/media artifacts (driven by the existing `BestFor` metadata). Text-only workflows
stay git-only. No user choice required for the common case.

### Implementation notes
- Split the strategy concept: `git` (local, zero-config default) vs `git_remote`/GitHub
  as the durable upgrade — or keep one `git` strategy with an optional `remote` field and
  treat "no remote" as the local default.
- On workflow creation, initialize a local git repo + first commit as the implicit
  default destination.

## Phased plan

- **Phase 1 — Remove Versions.** ✅ Done. Deleted UI + endpoints + `local_versions`
  strategy. `local_zip` export remains the offline fallback.
- **Phase 2 — Restore from Backup.** ✅ Done (restore-latest). Agent `restore` action +
  Restore button/confirm in the Backup popup. Git-commit picker deferred to Phase 4.
- **Phase 3 — History view.** ✅ Done. "Pulse" → "History" with Timeline + Plan edits
  sub-tabs; new `/api/workflow/plan-changelog` endpoint + `PlanChangelogFeed`.
- **Phase 4 — Link history ↔ restore.** Deferred. Associate changelog/decision entries
  with backup commits; "Restore to this point" from the timeline; auto-create a backup
  commit + decision entry before risky bulk operations. **Blocked on:** the backup agent
  does not yet stamp changelog entries with the commit it captured them in, and there is
  no hook that fires before harden/replan/bulk edits. Needs that linkage data first.

## Open questions
1. Phase 1 and Phase 2 should ship together — confirm we're not releasing the deletion
   before restore exists. (Strong recommendation: yes, together.)
2. Rename "Pulse" → "History", or keep "Pulse"?
3. ~~Zero-config local-git default~~ — **decided** (Decision 3): yes, local-git
   auto-commit is the default primary; GitHub/object_store are upgrades.
4. Do we surface `planning/changelog` to users at all, or keep it agent-only and only
   elevate `improve.html`? (Granular diffs may be noise for non-builders.)
5. One `git` strategy with optional `remote`, or split `git` (local) vs `git_remote`
   (GitHub)? Affects how the agent reasons about durability state.

## Non-goals
- No manual JSON editor for backup config (stays AI-driven — prior decision).
- Not building a general git client UI — only a commit picker scoped to the backup branch.
- Not auto-deleting existing `versions/` data.
