[← goals / step-execution](index.md)

# PLAT-328 — deterministic routing rejected an absolute run dependency inside its own workflow

| Field | Value |
|---|---|
| State | deployed |
| Priority | P1 |
| Product | goals |
| Area | step-execution |
| Summary | P1 — valid webhook runs reached their routing branch, then |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | `deployed as ancestor of f7c5f632b; RTS run-scoped live reverify pending` |
| Last synchronized | `2026-09-18` |

- **Priority:** P1 — valid webhook runs reached their routing branch, then
  failed before selecting the next step.
- **Owner:** shared workspace-path resolution used by trusted Go-side readers.
- **Related:** [PLAT-007](../../platform/integrations/plat-007.md) fixed the same canonical
  absolute-path mismatch for `read_image`; it did not cover deterministic
  routing, whose internal reader bypasses the agent-tool wrapper.

## Live evidence

RTS PR Reviewer webhook runs `iteration-40-hook` through
`iteration-42-hook` failed after `pr-eligibility-gate` successfully wrote
`route_selection.json`. The deterministic branch then tried to read the
resolved physical path:

`/data/video-studio/docs/Workflow/rtsprreviweer/runs/iteration-42-hook/default/execution/pr-eligibility-gate/route_selection.json`

Folder Guard correctly held a canonical `Workflow/rtsprreviweer` grant, but
the direct Go-side workspace reader passed the absolute form through without
normalizing it. Folder Guard therefore rejected an in-workspace file as
outside the granted path.

Earlier successful webhook runs did not disprove the bug: they bypassed this
branch by targeting `basic-pr-review`. The Builder's live workaround mirrors
the routing file to `db/assets/route_selection.json`; `iteration-43-hook`
then completed the skip route and `iteration-44-hook` crossed the eligible
branch. That workaround avoids the failing boundary but does not repair it.

The workaround later caused the separate concurrent-run race documented in
[PLAT-331](plat-331.md): iteration 68 branched for PR #87, then its review step
reread the shared mirror after iteration 69 overwrote it with PR #82. The
PLAT-328 code fix is present in deployed commit `f7c5f632b` through ancestor
`073245d6e`; the workflow mirror can now be removed, and the remaining live
acceptance should use the run-scoped dependency directly.

## Fix

`BaseOrchestrator.resolveWorkspacePath` now converts absolute paths beneath a
known workspace-docs root to the same canonical workspace-relative form used
by Folder Guard before applying workflow-prefix handling. This is a shared
boundary fix, so internal readers no longer need workflow-specific path
workarounds.

Absolute paths outside every known workspace root remain absolute and
continue to fail closed downstream. Regression coverage pins both the exact
RTS run-path shape and an outside path (`/etc/passwd`).

## Acceptance

- Focused and package-level orchestrator tests pass.
- Deploy the fixed binary to RTS.
- Exercise a webhook route whose `context_dependencies` resolves
  `route_selection.json` from a prior step's run directory without the
  `db/assets` mirror.
- Confirm deterministic routing reads it and Folder Guard still rejects an
  absolute path outside the workflow roots.
