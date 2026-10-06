[← goals / plans-contracts](index.md)

# PLAT-205 — the plan changelog is plan-mutation-only by design; document the scope boundary instead of extending it to generic file writes

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P3 |
| Product | goals |
| Area | plans-contracts |
| Summary | P3 (as documentation) — real gap in what artifact-drift |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `resolved` — documented as a scope boundary; no code change, by explicit user decision |
| Last synchronized | `2026-08-29` |

- **Priority:** P3 (as documentation) — real gap in what artifact-drift
  review can trust the changelog alone to reveal, but the plan changelog
  itself is working correctly for what it's actually scoped to (plan
  mutations), and extending it to cover arbitrary workspace file writes is
  a large, high-blast-radius change the user chose not to take.
- **Owner:** N/A — no code change. Guidance:
  `agent_go/cmd/server/guidance/templates/review/review-artifact-drift.md`.
- **Related:** Two harness findings, same `finding_id` (`PUL-FE64EF26`),
  filed under two different keys — `changelog:learnings-main.py-edits-uncaptured`
  and `harness:changelog:direct-file-write-gap` (confida-login, medium and
  low). Both self-classified `no_issue`/`changelog_coverage_gap` and both
  independently noted *"cannot be repaired from workflow tools; the
  changelog writer is platform-owned."* Distinct from PLAT-033/074/037
  (changelog truthfulness *for the plan-mutation tools it does cover*) —
  this ticket is about tools it was never wired to at all.

## The finding

`planning/changelog/changelog-2026-08-25-04-50-40.json` entry 3
(`update_scripted_step`, step `collect-change-context`) recorded only the
`plan.json` `description` field change. In the same turn,
`learnings/collect-change-context/main.py` (lines 568-578, a
`target_test_count` hardcode plus its explanatory comment) was also edited
— confirmed via direct file read — with no changelog entry anywhere
accounting for it. The sibling finding additionally confirmed
`db/README.md` hits the identical gap.

## What was confirmed in code

`writePlanChangelogEntry` (`planning_agent.go`) has exactly **two call
sites** in the entire codebase, both wired into specific typed plan-mutation
tools (`update_scripted_step` and siblings, `write_workflow_manifest`). It
is never called from `diff_patch_workspace_file` or `update_workspace_file`
— the generic tools actually used to edit `learnings/<step>/main.py` or
`db/README.md`. This is not an oversight in one call site; those tools have
no connection to the changelog mechanism at all.

**Also confirmed, and important context the user raised directly:**
`planning/` is never a granted write path for any tool other than the
specific plan-mutation tools (`workshopBlockedWritePaths`'s own doc comment,
`interactive_workshop_manager.go:2153-2168` — *"planning/ gets this for free
by not being a write path at all, which is why plan.json has always been
tool-only and therefore always recorded in the changelog"*). So `plan.json`
itself is fully and correctly captured, always — the gap is exclusively
about non-plan files (step code, shared docs) edited through generic
file-write tools, which were never in the changelog's scope to begin with.

## Disposition

Presented two options: extend the changelog to cover generic file writes
(the platform's single most heavily-used file-edit path — real blast
radius), or document the existing scope boundary. **User chose
documentation**, after clarifying precisely that plan.json's tool-only
enforcement is already correct and not itself in question — the actual
open question was narrower, about whether `main.py`/`db/README.md` edits
belong in the *same* audit trail at all.

Added a note to `review-artifact-drift.md`'s changelog-cursor step
clarifying: the changelog only records plan mutations; a direct code/doc
edit via `diff_patch_workspace_file`/`update_workspace_file` does not
produce an entry even in the same turn as a plan-tool call. Critically, the
checklist **already** directs inspecting `learnings/<step-id>/main.py`
directly for any step a changelog entry does flag as affected (step 3 of
the existing checklist) — so the code change is still caught whenever the
step enters review at all. The one residual, narrower gap: an *isolated*
code/doc edit with no accompanying plan-tool call in the same turn produces
no changelog entry, so nothing routes that step into the checklist's
cursor-selection step in the first place. That's now written down as an
explicit, understood scope boundary rather than something for a future
reviewer to rediscover as a fresh "changelog is broken" finding.

## Explicitly not done

- No code change. Wiring generic file-write tools into the plan changelog
  remains a real, available option if the residual isolated-edit gap proves
  costly in practice — deliberately deferred, not ruled out.
- Did not investigate the "existing partial mitigation" the findings
  mention (`changed_files` on Pulse fix-attempt records) in depth — noted as
  a separate, already-existing trail for code edits made through Pulse's own
  fix flow specifically, not a general-purpose replacement for the
  changelog.

## Verification

- `go build ./...` clean; `go test ./cmd/server/guidance/...`: same 3
  pre-existing failures before and after this edit (unrelated content), no
  regression.
