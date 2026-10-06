[← platform / learnings-knowledge](index.md)

# PLAT-223 — the managed-DB guidance no longer tells every step to read a file its own session may not be able to read

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | goals |
| Area | learnings |
| Summary | P1 — Upwork `PUL-EDFF0710` (per the |

| Coordination | Value |
|---|---|
| Assigned agent | Claude Code |
| Ticket state | `implemented; runtime reverify` |
| Last synchronized | `2026-08-29` |

- **Priority:** P1 — Upwork `PUL-EDFF0710` (per the
  [2026-08-29 triage audit](../../../../audits/pulse-platform-triage-2026-08-29.md)).
- **Owner:** injected step guidance vs. Folder Guard grant.
- **Severity:** low — no correctness impact was observed; both affected
  steps had already worked around the contradiction on their own via
  `query_workflow_db describe`. This is advisory-instruction noise, not a
  blocking defect.

## Problem

`BuildManagedWorkflowDBGuidance` (`agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/prompt_sections.go`),
injected into every agentic step with `db_access` read or read-write, ended
with an unconditional instruction: *"Read `db/README.md` before relying on a
table's business meaning."* The finding reproduced two cases where the
step's actual Folder Guard grant did not include that file at all —
`toptal-submit` (grant: `db/assets` only) and `profile-skill-harvest` (grant:
no `db/` path at all).

Investigated whether the fix should instead be "grant `db/README.md`
everywhere `db_access` implies this guidance." Ruled out as the wrong
boundary for this pass: ordinary execution steps (`setupExecutionFolderGuard`,
`controller_agent_factory.go:462`) already grant the *entire* `db/`
directory via `getDBPath`, which includes `db/README.md` — that path is
fine. The two affected steps evidently go through different, narrower
folder-guard setup functions (background/KB-harvest-style paths), and this
codebase has many distinct FolderGuard construction sites across step
types (execution, KB update/consolidate/reorganize, generic agents,
background reviewers, Pulse modules, the Builder session). Auditing every
one of them to guarantee this specific grant, without a live reproduction to
verify each, would be broad, slow, and easy to get subtly wrong. The finding
itself named the safer alternative: reword the boilerplate instead.

## Fix

Reworded both branches of `BuildManagedWorkflowDBGuidance` (read and
read-write) to never assert unconditional readability. The instruction now
says: read `db/README.md` first when it *is* readable in this session (still
the richer, preferred source for writer ownership and upsert-rule context
that schema alone doesn't carry), and use `query_workflow_db` with
`action: "describe"` — which is always available to any step that receives
this guidance in the first place — as the fallback when it is not.

This is a single-file, single-function change: it closes the contradiction
for every current and future step type at once, with no risk of missing a
FolderGuard construction site, and no behavior change for steps that already
have the grant.

## Verification

```text
go test ./pkg/orchestrator/agents/workflow/step_based_workflow/...
go test ./pkg/orchestrator/... ./cmd/server/...
```

New: `TestManagedDBGuidanceNeverRequiresDBReadmeUnconditionally` asserts,
for both access levels, that the old unconditional phrasing is gone, the
Folder Guard caveat is present, and the `query_workflow_db describe`
fallback is present. Full existing suite passes unchanged.

## Reverify

No live agentic step has rendered this guidance through the deployed server
since the change. Reverify by re-running a step whose Folder Guard grant
does not include `db/README.md` (e.g. a future `profile-skill-harvest`-style
run) and confirming it now follows the fallback instruction instead of
attempting an unreadable file.


## 2026-09-09 local Pulse audit follow-up

Build-in-public `PUL-69B66D52` still surfaced instructions requiring a DB README
outside the agent's available `db/assets` surface. The managed guidance fallback
is already implemented; this recurrence does not justify granting arbitrary raw
DB access. Acceptance must exercise the actual reviewer/step capability context
and reconcile stale workflow instructions with the canonical managed DB tools.
The separate root-manifest retention denial is now [PLAT-304](../../sandbox/security/plat-304.md)).
