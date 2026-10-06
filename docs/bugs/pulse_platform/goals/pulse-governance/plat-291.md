[← goals / pulse-governance](index.md)

# PLAT-291 — Preserve public finding IDs across automatic identity consolidation

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | goals |
| Area | pulse-governance |
| Summary | fixes a reproduced apparent non-persistence case: writes succeeded, but a later automatic merge deleted the returned issue ID. |

| Coordination | Value |
|---|---|
| State | Fixed locally; persistence/identity regression tests pass; not deployed |
| Date | 2026-09-05 |
| Evidence | Sales-outreach PUL-2F70A97F contains the complaint; its detail and later events also track a separate LinkedIn browser failure |

## Reproduced cause

A legacy harness finding can have the same target key but a different stored
fingerprint from the current canonical hash. `record_pulse_finding` created a new
row, persisted concern/detail/event records and returned its ID. A subsequent
backlog read ran identity consolidation, moved its evidence to the older
canonical row, then deleted the duplicate. The returned ID no longer resolved.

An isolated diagnostic on the production write/read functions confirmed rows in
all three tables before the backlog read, then zero rows under the returned ID
and a failed ID lookup afterward. Thus the write succeeded; identity loss made
it appear not to have persisted. The live report has an explicit
`filed:identity_merge:f96c8912` event, consistent with this mechanism. The exact
original PUL-7AC60412 call is not retained, so its history is not claimed proven.

## Repair

- Before writing a new typed finding, reuse the stored identity matching the
  same migration rules: explicit finding identity, or a harness-only target key.
  Unrelated workflow findings are not merged merely for sharing a target.
- Automatic merges preserve deleted rows' public IDs in the workflow-local
  `pulse_finding_issue_aliases` table before deletion. Further merges repoint
  aliases directly to the surviving fingerprint.
- Public ID resolution accepts those aliases; updates through an old ID write
  the surviving canonical row and return its current ID. Explicit semantic
  merge behavior remains unchanged.

Schema addition is additive and initialized by the existing lifecycle schema
path. No live workflow database was changed during implementation. Previously
deleted IDs cannot be reconstructed when their identity evidence is absent;
the implementation does not fabricate aliases for them.

## Verification and scope

Tests cover legacy identity reuse, persistence across repeated backlog reads,
case-insensitive IDs/targets, two successive merges, updating through the old
ID, preserved events, and unknown-ID rejection. Existing identity, typed-write,
semantic-merge and workflow-target isolation tests remain passing.

PUL-2F70A97F is **not closed**: its surviving canonical record also describes an
unresolved LinkedIn invite flow. This repair fixes the demonstrated platform
identity defect, not that browser behavior. No invitations were sent, no
historical business data was repaired, and no original report count changed.

## Register notes

[PLAT-291](plat-291.md) fixes a reproduced apparent non-persistence
case: writes succeeded, but a later automatic merge deleted the returned issue
ID. New writes reuse matching legacy identities and automatic merges preserve
public-ID aliases. Tests pass locally; not deployed. The mixed LinkedIn report
PUL-2F70A97F stays open, so the remaining report count is unchanged.
