[← goals / step-execution](index.md)

# PLAT-202 — PLAT-192 catches this reported JSON-registry partial-apply shape; broader partial-apply coverage remains unproven

| Field | Value |
|---|---|
| State | in progress |
| Priority | P3 |
| Product | goals |
| Area | step-execution |
| Summary | P3 — no live gap for this reported result shape. |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `resolved` — the exact reported incident shape is already covered; no broader mechanism claim |
| Last synchronized | `2026-08-29` |

- **Priority:** P3 — no live gap for this reported result shape. It does not
  establish that every possible partial apply is caught.
- **Owner:** N/A — no code change made or needed by this ticket.
- **Related:** [PLAT-192](plat-192.md)
  (`harness:diff_patch_workspace_file:silent-partial-apply`, first seen
  2026-08-24T18:24) — same failure shape, one day later, different file.
  This finding (`harness:diff_patch_workspace_file`, first seen
  2026-08-23T09:25) predates it and is best read as the *first* observed
  occurrence of the same bug class.

## The finding

A two-hunk diff against `knowledgebase/notes/_index.json` (hunk 1: append a
`covers[]` entry; hunk 2: replace `last_updated`/`last_updated_by`/
`section_count`/`size_bytes` while keeping a trailing `title` field as
context) returned `applied:true`. Hunk 1 applied correctly. Hunk 2 did
neither: the four scalar fields stayed at their old pre-patch values, and
the trailing `title` line was deleted entirely with no replacement — a
silent content loss the tool never surfaced as a failure.

This resembles the incident shape PLAT-192 guarded for a different
confida-login file: `applied:true` while a multi-hunk patch silently drops
part of its intended change and deletes unrelated trailing content. The
underlying apply mechanism remains unconfirmed.

## Verified: PLAT-192's shipped fix already catches this

Did not attempt to re-derive the internal apply-path mechanism that
produced this — PLAT-192 already tried and documented that as
unconfirmed ("Built a direct repro for the leading hypothesis... it did not
reproduce"). Instead, reproduced the **incident's own reported before/after
content** directly and ran it through `verifyDiffApplied`
(`workspace/handlers/diff_patch.go`, PLAT-192's line-count-delta invariant,
already live):

- The diff claims a net **+1** line delta (one `covers[]` append, one
  4-for-4 scalar replacement, net zero from the replacement).
- The reported broken result only changed by **+0** (scalars unchanged, one
  line vanished with no replacement).
- `verifyDiffApplied(original, diff, reportedBrokenResult)` correctly
  returns an error: *"diff apply produced an unexpected line-count change
  (diff claims +1 lines, result changed by +0) — refusing to report
  success."*

New permanent test:
`TestVerifyDiffAppliedCatchesTheReportedJSONRegistryIncidentShape`
(`workspace/handlers/diff_patch_test.go`), using the incident's own reported
values as fixture data rather than a hand-guessed reproduction.

## Explicitly not done

- No code change — PLAT-192's fix, already shipped this session, covers
  this incident's exact reported line-count shape. A corrupt result with the
  same net line delta is outside what this ticket verified.
- Did not merge this into PLAT-192's own ticket file — kept as a separate
  record since it was filed as a distinct harness finding with its own
  `target_key` and evidence, and the register's per-finding correction
  record convention (PLAT-195, PLAT-201) keeps that traceable.
- Did not further isolate whether the trigger is JSON content specifically,
  multi-hunk diffs generally, or something about the particular field
  dropped — PLAT-192 already scoped that as open and this ticket doesn't
  add new evidence on the mechanism, only on coverage.

## Verification

- `go build ./...` and `go test ./...` (workspace module) both clean.
- New test passes, using the incident's own reported before/after values.
