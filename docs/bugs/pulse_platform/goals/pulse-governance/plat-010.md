[← goals / pulse-governance](index.md)

# PLAT-010 — one logical finding can have two lifecycle fingerprints

| Field | Value |
|---|---|
| State | open |
| Priority | P1 |
| Product | goals |
| Area | pulse-governance |
| Summary | Pulse finding identity/deduplication |

| Coordination | Value |
|---|---|
| Assigned agent | `Codex` |
| Ticket state | `runtime_reverify` |
| Last synchronized | `2026-08-04` |

> Claim this ticket in this file before implementation. During active work,
> update this fragment rather than the shared index; synchronize the index
> once at handoff, review, or completion.


- **Priority:** P1
- **Owner:** Pulse finding identity/deduplication
- **Source finding:** `HARNESS-FINDING-FINGERPRINT-SPLIT`
- **Source database:** `Workflow/rtslatency/db/db.sqlite`
- **Problem:** two rows share finding ID
  `HARNESS-REFDOC-REVIEW-ARTIFACT-DRIFT` but have different fingerprints and
  split recurrence counts.
- **Impact:** closing one row leaves its twin open; Gate and reviewers keep
  rediscovering an issue already handled.
- **Current workaround:** reconcile every row with the same finding ID and
  semantic behavior together.
- **Implementation (2026-08-03):** structured `finding_id` is now the
  workflow-global lifecycle identity; reviewer module and wording no longer
  participate. Schema startup canonicalizes old single rows and merges twins,
  sums recurrence, moves attempts/verifications, preserves colliding events as
  explicit identity-merge events, and enforces one case-insensitive finding ID.
- **Completion (2026-08-04):** schema startup also finds event rows whose old
  fingerprint no longer has a detail row (the partial-migration shape observed
  in RTS), moves them to the canonical fingerprint, and deletes the orphan
  lifecycle address after preserving collisions as merge history.
- **Verification:** focused migration tests cover both a complete twin merge
  and an interrupted migration with a leftover `external_action_required`
  event. Real RTS backlog projection remains a runtime reverify.
- **Regression tests:** `TestFindingIdentityMigrationMergesTwinsAndPreservesEvents`
  and `TestFindingIdentityMigrationMovesEventsLeftByPartialMigration`; the full
  step-based workflow package passes on 2026-08-04.
- **Acceptance:** one stable platform issue can link several observations and
  fingerprints, while one workflow lifecycle row cannot claim the same
  finding ID twice. Migration merges existing twins without deleting events.
