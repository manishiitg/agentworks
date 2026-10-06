[← sparkquill / integrations](index.md)

# PLAT-121 — split SparkQuill into its own release process

| Field | Value |
|---|---|
| State | deployed |
| Priority | P3 |
| Product | sparkquill |
| Area | integrations |
| Summary | `.github/workflows/sparkquill-desktop.yml`, |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `implemented` — CI-verified, no further work identified |
| Last synchronized | `2026-08-16` |

- **Priority:** P3
- **Owner:** `.github/workflows/sparkquill-desktop.yml`,
  `.github/workflows/desktop-release.yml`

## Problem (as originally raised)

SparkQuill (the family-learning desktop app) and AgentWorks Desktop shipped
through the same release pipeline, risking one app's change blocking or
re-releasing the other.

## Current state

This is already done, contrary to how it was carried on the task list.
`sparkquill-desktop.yml` exists as a fully separate workflow:

- Its own tag namespace (`sparkquill-v*`, distinct from AgentWorks' `v*`) —
  git history shows this was itself a bug fix
  (`bf5b8fca Fix SparkQuill release publishing to use its own tag namespace`).
- Path-scoped PR triggers (`desktop-sparkquill/**`,
  `agent_go/cmd/family-server/**`, `frontend/learning-app/**`, its own
  workflow file) so unrelated AgentWorks changes don't trigger it.
- A comment in the file states the intent directly: *"Separate from 'Desktop
  DMG' (AgentWorks) on purpose: the two apps ship independently, on their own
  tags, and neither should be blocked or re-released by a change to the
  other."*
- The 5 most recent `main`-branch CI runs are green (`gh run list
  --workflow=sparkquill-desktop.yml`), most recent success
  2026-08-16T08:31:42Z.

An earlier flaky failure (an unrelated Swift voice-helper cache issue that
blocked PR #182) was transient — not evidence the split itself is incomplete.

## Acceptance

- A SparkQuill-only change triggers only `sparkquill-desktop.yml`. ✅ (path
  filters)
- A SparkQuill tag never collides with or is blocked by an AgentWorks
  release. ✅ (separate tag namespace, separate concurrency group)
- Recent CI history is green. ✅

No further action identified; closing candidate pending anyone spotting a gap
this pass missed.
