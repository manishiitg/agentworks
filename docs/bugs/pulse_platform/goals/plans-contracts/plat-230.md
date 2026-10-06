[← goals / plans-contracts](index.md)

# PLAT-230 — changelog coverage and the retired `builder/improve.html` cursor were both already settled

| Field | Value |
|---|---|
| State | closed |
| Priority | P2 |
| Product | goals |
| Area | plans-contracts |
| Summary | P2 — LinkedIn `PUL-17E6F19A`, `PUL-7607952E` (per the |

| Coordination | Value |
|---|---|
| Assigned agent | Claude Code |
| Ticket state | `resolved — both findings already settled by prior decisions/restructuring` |
| Last synchronized | `2026-08-29` |

- **Priority:** P2 — LinkedIn `PUL-17E6F19A`, `PUL-7607952E` (per the
  [2026-08-29 triage audit](../../../../audits/pulse-platform-triage-2026-08-29.md)).

## `PUL-17E6F19A` (`HARNESS-CHANGELOG-COVERAGE-001`) — same root cause as PLAT-205, already resolved

The finding: "Artifact Review's canonical changelog omits evaluation and
learning mutations," reproduced against `Workflow/linkedin`'s 2026-07-28
evidence (an evaluation-plan edit and a learning edit, neither producing a
changelog entry).

This is the identical mechanism [PLAT-205](plat-205.md) already
investigated and closed: `writePlanChangelogEntry` has exactly two call
sites, both wired to specific typed plan-mutation tools, never to the
generic `diff_patch_workspace_file`/`update_workspace_file` paths actually
used to edit `learnings/<step>/main.py`, `db/README.md`, or (this finding's
case) `evaluation/evaluation_plan.json`. PLAT-205 presented the two real
options — extend the changelog to cover generic file writes, or document the
existing scope boundary — directly to the user, who **explicitly chose
documentation**, and that note already lives in
`review-artifact-drift.md`'s changelog-cursor step. This finding did not
surface a new mechanism or a case PLAT-205's fix doesn't cover; it is the
same gap observed on a different workflow, filed before PLAT-205 closed and
never reconciled against it. No further platform action needed beyond what
PLAT-205 already did.

## `PUL-7607952E` — the `builder/improve.html` Artifact Sync Cursor it describes no longer exists

The finding (`run_concerns` text only, no structured detail row survived):
*"builder/improve.html has lost the Artifact Sync Cursor that the 2026-07-28
fixer reconciled, so review coverage has no current canonical boundary."*

Checked directly against the live workflow rather than the finding's
snapshot: `workspace-docs/Workflow/linkedin/builder/` today holds
`card.*.html`, `review.html`, `decisions.jsonl`, and related files —
**no `improve.html`**. LinkedIn's `workflow.json` is at contract version
`1.0.31`. [PLAT-055](../../platform/learnings-knowledge/plat-055.md)) documents a mandatory version-upgrade
preflight (`workflowContractArtifactPurityVersion`, "1.0.21") that moves
`builder/improve.html` and `builder/improve-archive/` into
`migration-backups/artifact-purity-<timestamp>/` for every workflow as it
passes through that version — every one of the 21 workflows on the platform
was blocked on this preflight at the time PLAT-055 shipped it. LinkedIn, ten
contract versions past 1.0.21, has long since gone through it. The entire
"Artifact Sync Cursor in `builder/improve.html`" mechanism this finding asks
to have restored was deliberately retired platform-wide, independent of this
finding, before it could even be actioned. There is nothing to sync a cursor
into any more.

## Verification

N/A — no files changed. Both are closure/reclassification records: one
pointing at an existing resolved ticket, one confirming the referenced
mechanism was already retired.
