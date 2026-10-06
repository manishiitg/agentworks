[← goals / pulse / findings](index.md)

# PLAT-222 — a Pulse finding's target_key is now scoped by reporting module, so two modules can no longer silently overwrite each other's finding

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | goals |
| Area | pulse/findings |
| Summary | P1 — Sales Outreach `PUL-1E38F625` (per the |

| Coordination | Value |
|---|---|
| Assigned agent | Claude Code |
| Ticket state | `implemented; runtime reverify` |
| Last synchronized | `2026-08-29` |

- **Priority:** P1 — Sales Outreach `PUL-1E38F625` (per the
  [2026-08-29 triage audit](../../../../../audits/pulse-platform-triage-2026-08-29.md)).
- **Owner:** Pulse finding identity / `record_pulse_finding` dedup.
- **Related:** distinct from [PLAT-206](plat-206.md) (a terminal-module-result
  write collision, not an identity collision).

## Problem

`record_pulse_finding` deduplicated a **new** `technical_review` finding into
an **existing** `strategic_review` issue purely because both happened to
choose the same `target_key` string, silently overwriting the
`strategic_review` issue's summary/concern/impact/evidence/classification
with the unrelated `technical_review` content — while leaving the issue's
owning-module attribution unchanged as `strategic_review`, producing a
lifecycle row whose content and module disagreed. No error, warning, or
cross-module confirmation was raised.

Root cause: `pulseFindingCanonicalFingerprint`
(`agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/pulse_finding_details.go`)
computed the canonical fingerprint for any structured `target_key` identity
as `concernFingerprint("__structured_finding__", "target_key:"+target_key)` —
a fixed, module-agnostic scope, by design, per its own prior comment
("Structured IDs are workflow-global identities. Including the reporting
module here recreated the same issue once per reviewer.").

That design intent is correct for `harness_issue` findings: their
`target_key` is a deliberately shared, cross-workflow platform identity (the
same convention the cross-workspace harness registry relies on —
`TestHarnessFindingPlatformRegistryDeduplicatesAcrossWorkflows` — and the one
this fix does not change). It is wrong for every other kind. A
`workflow_issue`'s `target_key` is just a workflow-local location reference
(a step, a table, a config key) that different reviewer modules routinely
reuse to name entirely unrelated concerns about the same location —
`technical_review` and `strategic_review` both citing
`planning/plan.json:step-prepare-linkedin-engagement` for two different
reasons, in the reported case. There is no cross-module identity convention
for that string at all; the collision was accidental.

## Fix

`pulseFindingCanonicalFingerprint` now scopes by reporting module (`stepID`)
for every issue kind except `harness_issue`, and only for `target_key`-based
identity:

- `finding_id`-based identity is unchanged and stays module-agnostic
  regardless of kind. Choosing an explicit `finding_id` is itself a
  deliberate, author-asserted claim of "this is the same finding," meant to
  survive rewording *and* a change of reporting module — exactly what
  `TestStructuredFindingIDSurvivesRewordingAndReviewerChange` already pinned,
  and continues to pin.
- `target_key`-based identity for `harness_issue` stays module-agnostic —
  unchanged, still the deliberately shared cross-workflow platform identity.
- `target_key`-based identity for every other kind (`workflow_issue`) is now
  scoped by the reporting module, so two different modules naming the same
  location can never collapse onto the same fingerprint, and one module can
  never silently overwrite another module's finding.

## Verification

```text
go test ./pkg/orchestrator/agents/workflow/step_based_workflow/...
go test ./pkg/orchestrator/... ./cmd/server/...
```

New coverage: `TestDifferentModulesSharingATargetKeyDoNotCollide` reproduces
the exact PUL-1E38F625 shape end to end through the live `RecordPulseReviewFinding`
path (no executor called directly) and asserts both findings survive with
their own content and module attribution intact.
`TestSameModuleTargetKeyStillDedupsAcrossPulseRuns` proves the fix did not
turn `target_key` dedup into "always a new issue" — the *same* module across
two Pulse runs still converges on one issue.
`TestHarnessIssueTargetKeyStillDedupsAcrossModules` is a positive control
protecting the deliberately preserved `harness_issue` cross-module dedup.
The full existing suite passes unchanged, including the `finding_id`
cross-module-survival test that first caught an overly broad version of this
fix during development (it originally scoped `finding_id` by module too,
which the test suite correctly rejected).

## Reverify

No live Pulse review turn has exercised this fix through the deployed
server yet. Reverify by reproducing the original two-module,
shared-`target_key` scenario against a real workflow and confirming both
findings persist independently.
