[← goals / plans-contracts](index.md)

# PLAT-012 — changelog coverage excludes material dependent artifacts

| Field | Value |
|---|---|
| State | open |
| Priority | P2 |
| Product | goals |
| Area | plans-contracts |
| Summary | managed mutation audit/changelog |

| Coordination | Value |
|---|---|
| Assigned agent | `Claude Code` |
| Ticket state | `runtime_reverify` |
| Last synchronized | `2026-08-04` |

> Claim this ticket in this file before implementation. During active work,
> update this fragment rather than the shared index; synchronize the index
> once at handoff, review, or completion.


- **Priority:** P2
- **Owner:** managed mutation audit/changelog
- **Source finding:** `HARNESS-CHANGELOG-COVERAGE-001`
- **Source database:** `Workflow/linkedin/db/db.sqlite`
- **Problem:** evaluation-plan and learning mutations were absent from the
  canonical changelog Artifact Review uses.
- **Impact:** a grading-contract or runtime-guidance change can escape dependent
  artifact review.
- **Implementation (2026-08-03):** every managed changelog entry is completed
  with a canonical target, before/after SHA-256 refs, actor, and dependency
  class. Evaluation-plan edits already use the managed tool; runtime learning
  turns now hash the complete `learnings/_global` tree (including references)
  before and after the serialized write turn and append a typed mutation event
  whenever the tree changes, even if the turn itself later reports an error.
- **Acceptance:** every sanctioned material mutation emits one typed changelog
  event with target, before/after references, actor, and dependency class.
