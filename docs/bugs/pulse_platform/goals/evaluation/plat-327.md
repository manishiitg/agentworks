[← goals / evaluation](index.md)

# PLAT-327 — Cross-install plan dependency compatibility and governed evaluation-step creation

| Field | Value |
|---|---|
| State | deployed |
| Priority | P1 |
| Product | goals |
| Area | evaluation |
| Summary | LinkedIn `PUL-286AF1C9` supplied producer step ID |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | `implemented locally; deploy and runtime reverify` |
| Last synchronized | `2026-09-17` |
| Priority | `P1 reliability` |

## Evidence

- LinkedIn `PUL-286AF1C9` supplied producer step ID
  `step-engagement-performance` in `context_dependencies` while the producer's
  declared artifact is `performance_checked.json`. The old resolver treated
  the ID as a literal filename and constructed a nonexistent path.
- Substack `PUL-0B771D46` had an empty evaluation plan. The governed surface
  could update or delete an existing evaluation step but could not create the
  first one.
- Sales Outreach `PUL-0454CE5A` reported legacy scripted-source selection under
  `code_layout_version=1`. Current runtime code already uses the centralized
  layout resolver, so the missing protection was regression coverage proving
  version 1 never falls back to `learnings/`.

## Implemented fix

1. Dependency resolution now recognizes a legacy producer-ID dependency and
   resolves it to that producer's declared `context_output`. This compatibility
   behavior applies in normal and full-workflow execution after the same server
   build is deployed; no laptop-local workflow edit is required to avoid the
   runtime failure.
2. Plan Drift adds deterministic `context_dependency_filenames` evidence and
   flags the legacy plan shape for canonical cleanup. New plans should continue
   to store artifact filenames, not step IDs.
3. Added governed `add_evaluation_step`, including required rationale, schema
   validation, cross-plan ID-collision rejection, atomic write, complete added
   JSON in `planning/changelog`, Workshop registration, and tool-surface tests.
4. Added regression coverage proving `code_layout_version=1` reads only
   `code/<step-id>/main.py` and does not accept a legacy-only script.

## Deployment boundary

The implementation is platform code, not a patch to one LinkedIn folder. It
becomes effective on each laptop or server when that host receives the rebuilt
backend. Workflow data does not automatically replicate between hosts; any
host-specific plan can retain the old spelling, but the deployed runner remains
compatible and its next due Plan Drift review will flag it for cleanup.

## Verification

- Full `step_based_workflow` package passes.
- Focused `cmd/server` tool-set invariant passes.
- Runtime reverify remains: run the LinkedIn consumer once on a deployed build,
  create the first eval step in an empty plan through Workshop, and run one
  version-1 scripted step with no legacy copy present.
