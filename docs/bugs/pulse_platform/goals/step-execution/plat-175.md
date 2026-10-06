[← goals / step-execution](index.md)

# PLAT-175 — a fix that blocked raw `db.sqlite` filesystem access for message_sequence steps also dropped `db/assets/`, the one durable location a step can write an arbitrary file to

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | goals |
| Area | step-execution |
| Summary | P1 — silent, not a crash: the affected step reports success |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `fixed` — build/test verified, live reverify pending on a fresh run |
| Last synchronized | `2026-08-21` |

- **Priority:** P1 — silent, not a crash: the affected step reports success
  either way, because nothing surfaces the missing grant until the step
  actually needs to write. `confida-login`'s own reflection turn caught it
  first, as a concern, before any file operation failed loudly.
- **Owner:** `pkg/orchestrator/agents/workflow/step_based_workflow/controller_message_sequence.go`
  (`setupMessageSequenceFolderGuard`).
- **Related:** none filed. The code comment this fix rewrites cited
  "PLAT-169 follow-up" as the origin of the `db/` block being narrowed here
  — that citation was wrong. [PLAT-169](../../app/ui/plat-169.md)) is a real, unrelated
  ticket (MCP server checkbox spelling/dedup). The actual `db/` block was
  introduced by commit `a960df20` ("Fix message sequence sandbox and
  duplicate failures", 2026-08-21), which has no PLAT ticket of its own —
  the mislabel is corrected in both the code comment and here, rather than
  leaving a false cross-reference for the next reader to trust.

## The incident

`confida-login`'s `survey-app-and-refresh-knowledge` step is a
`message_sequence` step. Its own plan description (`plan.json`) instructs it,
every cycle, independent of the browser survey:

> *Compare [the upstream repo's head SHA] to
> `db/assets/business-context/.source_sha`. If it changed ... pull the
> current tree and refresh `db/assets/` to match the repo exactly — add or
> overwrite changed files ..., remove local files no longer present ..., and
> update `.source_sha` and `_manifest.json`.*

This needs shell-level read (to compare `.source_sha`, list what's already
there) and write (to sync files, rewrite `.source_sha`/`_manifest.json`)
access to `db/assets/business-context/`. `mutate_workflow_db` cannot do this
— it is SQL-only, and this is arbitrary file sync.

The step's own reflection turn flagged it on 2026-08-21: it had no legal path
to touch `db/assets/` at all. It didn't fail loudly because nothing upstream
had changed on the runs where this was checked, so there was nothing to
write — the gap is real but was latent, not yet observed as a hard failure.

## Root cause

Two functions build a step's folder-guard scope depending on step type.
Regular steps go through `setupExecutionFolderGuard`
(`controller_agent_factory.go:449-455`), which grants the whole `db/` folder
to both read and (when write-eligible) write paths — this covers
`db/assets/` for every ordinary step.

`message_sequence` steps go through a different function,
`setupMessageSequenceFolderGuard`, which — before this fix — granted nothing
under `db/` at all. Its own comment explained why, correctly, for one specific
concern: `configureWorkflowDBSession` blocks raw `db.sqlite` filesystem
access for managed agents, and Landlock's rules are additive, so it cannot
express "allow the `db/` parent, deny only the `db.sqlite` child" — commit
`a960df20` therefore dropped the parent grant (`db/`) entirely to sidestep
that conflict.

That change was correct about `db.sqlite`. It was too broad about `db/`:
`db/assets/` is a **sibling** of `db.sqlite`, not a child of it — granting
`db/assets/` directly does not require also allowing `db.sqlite`, so it never
needed to be part of the same trade-off. Dropping the whole `db/` grant to
solve a `db.sqlite`-specific Landlock problem took `db/assets/` down as
unintended collateral, for every `message_sequence` step, not just this one.

## Fix

`setupMessageSequenceFolderGuard` now grants `db/assets/` specifically —
`filepath.Join(getDBPath(baseWorkspacePath), DBAssetsFolderName)` — to both
`readPaths` and `writePaths`, unconditionally (matching `resolveDBAccess`'s
existing uniform-access design: every step gets managed DB read-write, so
there is no separate access level to gate this on). `db.sqlite` itself
remains ungranted; commit `a960df20`'s actual fix (blocking raw `db.sqlite`
filesystem access) is untouched.

## Deliberately not done

- **Not restoring full `db/` filesystem access for message_sequence steps.**
  That would reopen exactly the Landlock conflict `a960df20` fixed. `db/assets/`
  is narrow enough to avoid it because it never shared a Landlock rule with
  `db.sqlite` in the first place.
- **Not writing up this asymmetry in a design doc as part of this pass.**
  Checked `docs/workflow/persistent_stores_design.md` §3 and
  `docs/core/folder_guard_system.md` — neither documents the
  regular-vs-message_sequence write-path split; it exists only as the code
  comment this fix rewrites. Worth a short addition to one of those docs as a
  follow-up so a future reader doesn't have to trace both folder-guard
  functions side by side to find this, the way answering it here required.

## Verification

- `TestMessageSequenceFolderGuardGrantsDBAssetsReadWrite` — new. Asserts
  `db/assets/` is present in both read and write paths for a message_sequence
  step, and that `db.sqlite` is not exposed by that grant. Confirmed failing
  before the fix (missing from both path sets) and passing after, by
  reverting only the production change via a scoped `git stash` and
  re-running.
- `TestMessageSequenceItemUsesManagedDBToolsWithoutRawDBFilesystemAccess` —
  existing regression test for commit `a960df20`'s `db.sqlite` block, narrowed
  from a blanket `"/db"` substring check (which would now incorrectly flag the
  legitimate `db/assets/` grant) to specifically checking for `db.sqlite` and
  any `db/` path outside `assets/`. Still passes, still catches the original
  failure mode.
- Full `message_sequence`-prefixed test set (31 tests) and the full
  `step_based_workflow` package pass, with the one pre-existing, unrelated
  failure already tracked elsewhere in this register
  (`TestRunInBackgroundPassesBuilderSkillSnapshotToBothAgentKinds`).
- `go build ./...` clean.

Not yet reverified live: the direct signal is `survey-app-and-refresh-knowledge`
actually writing to `db/assets/business-context/` on a future cycle where the
upstream context repo has genuinely changed.

## Acceptance

- [x] `message_sequence` steps can read and write `db/assets/` via shell.
- [x] `message_sequence` steps still cannot reach `db/db.sqlite` via shell
      (commit `a960df20`'s guarantee holds).
- [ ] Live: `survey-app-and-refresh-knowledge` successfully syncs
      `db/assets/business-context/` on a cycle where the upstream repo
      actually changed.
