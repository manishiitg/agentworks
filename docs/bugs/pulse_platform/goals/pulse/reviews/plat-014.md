[← goals / pulse / reviews](index.md)

# PLAT-014 — reviewer prompts named unavailable reference documents

| Field | Value |
|---|---|
| State | in progress |
| Priority | P1 |
| Product | goals |
| Area | pulse/reviews |
| Summary | reviewer reference/skill delivery |

| Coordination | Value |
|---|---|
| Assigned agent | `Claude Code` |
| Ticket state | `partial_runtime_reverify` |
| Last synchronized | `2026-08-04` |

> Claim this ticket in this file before implementation. During active work,
> update this fragment rather than the shared index; synchronize the index
> once at handoff, review, or completion.


- **Priority:** P1
- **Owner:** reviewer reference/skill delivery
- **Source finding:** `HARNESS-REFDOC-REVIEW-ARTIFACT-DRIFT`; legacy copies in
  RTS Latency and Tectonicus, plus a Tectonicus Goal Advisor variant
- **Current state:** **reverify**. Reviewer guidance has migrated from
  `get_reference_doc(kind=...)` to attached skills loaded with `read_skill`.
- **Impact:** old reviewers started without their required method and silently
  substituted a different checklist.
- **Acceptance:** each scheduled and slash-command reviewer loads every named
  skill/reference in its isolated stage session; no retired
  `get_reference_doc` instruction remains on a live path.
