---
name: release-candidate-to-reviewed-gate
description: Propose an exact-build QA journey and release gate handoff with optional flake investigation.
---

# Release Candidate to Reviewed Gate

## Outcome

Produce an attempt-level journey result and a reviewed release gate proposal for the exact SHA, build, and environment. A proposed pass, owner approval, provider-published status, merge, and deployment are distinct states.

## When to use

Use when a small team needs a repeatable release quality decision from browser and CI evidence. For a single QA chat question, one Crew may be enough. Add the Flaky Test Investigator only for an intermittent result requiring its own evidence and owner.

## Discovery and user direction

Inspect existing QA Crews, candidate identity, policy revision, required suites, attempts, evidence retention, owners, and status destination. Propose reuse or reviewed creation. Show the exact route before Builder applies it; selection leaves setup pending.

## Required inputs

Bind release or PR ID, SHA, immutable build artifact, environment, cutoff, approved journey revision, required suite policy, test account scope, gate owner, and status authority.

## Plan and AgentWorks tools

Reuse Browser Journey QA Analyst and Release Quality Assistant. Journey writes `journey-result/v1`; run the bundled validator before Gate consumes it. If attempts conflict, route a bounded artifact to Flaky Test Investigator, validate its `flake-investigation/v1`, and keep original failures visible. Gate re-reads all required suite records and writes `release-quality-brief/v1`. Insert explicit validator steps in the Workflow. Publish only the exact owner-approved decision through a separately authorized route.

## Knowledge and persistence

Save release, SHA, build, environment, policy, journey, suite, attempt, source, reviewer, approval, provider receipt, and run IDs. A new SHA or build is a new gate decision. Reruns preserve prior attempts and waivers.

## Validation and reporting

The [validator](scripts/validate_handoff.py) checks identity, required suite coverage, evidence, flake disposition, verdict, and publication receipt. Fictional [journey](examples/journey-result.json) and [gate](examples/release-quality-brief.json) pass; the [wrong-build pass](examples/invalid-release-quality-brief.json) fails. The [flake investigation](examples/flake-investigation.json) pairs with a [needs-review gate](examples/flake-needs-review-gate.json); [owner approval](examples/approved-release-quality-brief.json) and [status receipt](examples/status-publication.json) are later artifacts. The reporting dashboard shows tested candidate, required suites, failures, blocked and stale evidence, owner decision, published state, and cost separately.

## Guardrails

Do not hide failures behind retries, weaken assertions, waive suites, publish a status, merge, or deploy through installation. A green CI summary is insufficient without exact candidate and required suite joins. Verify any exception with an authorized reviewer and source record.

## Read details when needed

- [Workflow design and outcomes](../../references/workflow-design-and-outcomes.md) for route decisions.
- [Team and handoffs](references/team-and-handoffs.md) for QA contracts and [setup](SETUP.json) for ten checks.

## Completion contract

Return Crew IDs, source map, validated artifacts, full suite matrix, owner decision, publication receipt or none, manual run, activation choice, and blockers. Keep recurrence off until reviewed.
