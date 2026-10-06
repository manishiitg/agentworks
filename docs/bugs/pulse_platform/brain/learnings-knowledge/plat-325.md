[← brain / learnings-knowledge](index.md)

# PLAT-325 — Cross-workflow knowledgebase write access (PLAT-310 follow-up)

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | brain |
| Area | learnings-knowledge |
| Summary | Implemented locally, not yet deployed to RTS: `knowledgebase_sources` now accepts `access: "write"` (confined to the source's `notes/` folder), gated by an explicit `kb_write_grants` consent from the source workflow's o… |

| Coordination | Value |
|---|---|
| Assigned agent | Claude Code |
| Ticket state | `implemented; not deployed` — build/test verified locally, RTS deployment pending |
| Last synchronized | `2026-09-17` |
| Implementation commits | `58f5a341c` (backend), `43f6c7118` (frontend + docs) |
| Related | [PLAT-310](plat-310.md) — the read-only feature this extends. Base ticket's own "Later extensions" section named this exact gap: *"Shared contribution would require an explicit contribution mode, eligible writers, notes-only write paths, provenance, conflict handling, and serialization keyed to the source KB across workflows."* |

## Scope

PLAT-310 shipped read-only cross-workflow KB attachment
(`knowledgebase_sources`, `access: "read"` only). Read-only was too narrow: a
workflow that discovers something useful while reading another's KB had no
way to contribute it back. This ticket adds `access: "write"`, confined to
the same `notes/` boundary local KB writes already use, gated by explicit
two-sided consent rather than the audience-overlap check that governs read.

Full design, configuration, runtime mechanics, and acceptance criteria are
maintained in [`docs/workflow/shared_knowledgebase_sources.md`](../../../../workflow/shared_knowledgebase_sources.md)
(updated in place for this ticket, not duplicated here) — see its "Write
access" section. This ticket file covers the incident/investigation trail
and disposition; the design doc is the living contract.

## Investigation (before implementation)

Two design questions were resolved by direct code investigation before
writing anything:

1. **Does the existing local-write boundary generalize?** Yes — local KB
   writes were already confined to exactly `knowledgebase/notes/` (never the
   KB root, never `context/`) via three separate folder-guard builders
   (`setupExecutionFolderGuard`, `setupMessageSequenceFolderGuard`,
   `setupOrchestratorFolderGuard` — one per execution shape, all gated by the
   same `kbAccessAllowsWrite` check). Extending cross-workflow write reuses
   the identical mechanism, pointed at a resolved external path instead of
   the local one — no new write primitive needed.
2. **Is concurrent writing from multiple workflows' sessions safe today?**
   No — KB writes are plain `diff_patch_workspace_file`/`update_workspace_file`
   calls with zero locking, fine for one workflow's own sequential turns,
   unsafe once a second workflow's session can write into the same folder
   concurrently. **Explicitly scoped out of this pass by user decision** — see
   "Deliberately not done" below.

Also checked: PLAT-272 shared workflow secrets, considered as a possible
precedent for a "workflow grants another workflow X" consent pattern. It
isn't one — it's cross-*user*, same-workflow secret sharing, not
cross-*workflow* consent. No existing target-side allowlist pattern existed
anywhere in the codebase; `WorkflowAccess.AllowedKBWriters` is new.

## Fix

- **Schema**: `KnowledgebaseSource.Access` accepts `"write"` (implies read)
  alongside `"read"`. New `WorkflowAccess.AllowedKBWriters []string` (workflow
  IDs) on the *target's* manifest — the consent step a consumer's write
  source depends on.
- **Authorization**: `workflowkb.AudienceCanWrite` requires both the ordinary
  read-audience check (`AudienceCanRead`) and explicit membership in the
  target's `AllowedKBWriters`. Audience overlap alone, sufficient for read,
  is not sufficient consent to mutate another workflow's KB.
- **Runtime**: all three folder-guard builders now also append the resolved
  external write source's `notes/` path, behind the exact same
  `kbAccessAllowsWrite` gate as the local grant — no separate bypass.
- **Config surface**: `update_workflow_config(knowledgebase_sources=...)` for
  the consumer side, `update_workflow_config(kb_write_grants=...)` for the
  grantor side (new), both mirrored in the workflow manifest HTTP API and the
  `KnowledgebaseSources.tsx` frontend (attach-with-write-choice, badges, and
  a new grantor-side `KBWriteGrants` panel).
- **Provenance**: the KB-sources prompt now requires a contributed note to
  tag the writing workflow's ID.

## Deliberately not done

- **Concurrency/locking.** By explicit user decision, not built in this
  pass. Two workflows' sessions writing into the same target `notes/` folder
  at the same moment can race at the OS file-write level. `learningsGlobalFileMutex`
  (`agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/learnings_direct.go`)
  is the precedent to mirror if this needs solving later: an in-process mutex
  keyed by the target's canonical `notes/` path (not one global lock), held
  for the duration of the write turn the same way
  `prepareDirectLearningTurn` holds its mutex. That mutex's own doc comment
  already discloses the same in-process-only limitation, which is why it's
  the right precedent rather than something to invent fresh.
- **A hard hard-gate forcing message_sequence steps to load plan-design
  guidance before authoring their first step** — unrelated tangent explored
  in the same session, explicitly abandoned: it would require modifying
  `mcpagent` (shared cross-product runtime, not contained to this repo) to
  track tool-call history, and a rigid rule would itself be wrong sometimes
  (a deterministic fetch-first step is legitimately correct). Not part of
  this ticket's scope; noted here only because it was investigated adjacent
  to this work and explicitly rejected as its own path.

## Verification

- New Go tests: `TestSourcesWriteAccessRequiresExplicitGrant` (`pkg/workflowkb`)
  — no-access-block, audience-overlap-without-allowlist, explicit-grant, and
  revocation cases. `TestKBSourcesWriteGrantAppliesToAllThreeFolderGuards`
  (`step_based_workflow`) — proves all three folder-guard builders grant the
  external write path once granted, and that a read-only step's own
  `kbAccess` still blocks it (no separate bypass).
- `go build ./...` and `go test ./...` clean across `pkg/workflowkb`,
  `pkg/workflowtypes`, `pkg/orchestrator/agents/workflow/step_based_workflow`,
  `cmd/server`, `cmd/server/guidance`.
- `npx tsc -b --force` clean; existing `KnowledgebaseSources.test.tsx` suite
  passes (one interaction bug caught and fixed during this work: the new
  `KBWriteGrants` form's DOM position initially preceded the existing
  attach-source form, breaking a test's `querySelector("form")` assumption —
  reordered so the pre-existing form stays first in document order).
- Not yet done: live end-to-end verification with two real workflows on a
  deployed host (grant, attach, write, confirm provenance tag, confirm a
  non-granted third workflow is refused).

## Acceptance

- [x] `access: "write"` accepted by schema validation; `"read"` still the
      default and still read-only.
- [x] Write requires explicit `kb_write_grants` from the target; audience
      overlap alone is not sufficient (verified: revoking the grant while
      audience overlap remains intact revokes write).
- [x] Write confined to `notes/`; KB root and `context/` remain untouched by
      this grant.
- [x] All three execution shapes (plain step, message_sequence item,
      orchestrator's own turn) honor the grant identically.
- [ ] Live: two real workflows on a deployed host, end-to-end grant → attach
      → write → read-back cycle.
- [ ] Concurrency/locking — explicitly deferred, not an acceptance item for
      this ticket.

## Register notes

Implemented locally, not yet deployed to RTS: `knowledgebase_sources` now
accepts `access: "write"` (confined to the source's `notes/` folder), gated
by an explicit `kb_write_grants` consent from the source workflow's own
owner rather than the audience-overlap check read relies on. Concurrent
writers into the same target are explicitly not serialized in this pass —
a known, accepted gap, not an oversight. See
[PLAT-325](plat-325.md) and the updated
[shared KB contract](../workflow/shared_knowledgebase_sources.md).
