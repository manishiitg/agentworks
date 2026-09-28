---
name: access-exception-to-verified-fix
description: Propose an authorized permission exception to owned remediation and independent same-cell retest.
---

# Access Exception to Verified Fix

## Outcome

Produce a policy-bound access exception, then track an approved fix and independent same-cell retest. Keep proposal, approval, merge, deployment, retest and closure distinct.

## When to use

Use when a SaaS team needs to follow a reproducible role, ownership or cross-tenant mismatch through remediation. Use one Access Review Analyst for a standalone permission matrix. Use Finding to Verified Remediation for a general scanner or application finding without an exact permission cell.

## Discovery and user direction

Inspect scope, policy revision, actors, fixtures, build, evidence rules, existing Crews, change sources and closure authority. Reuse compatible Crews or propose creation. Show the route before Builder applies it; installation leaves setup pending.

## Required inputs

Bind tenant, policy revision, exact actor role and tenant, resource type, resource tenant and ID, action, expected decision, build, environment, direct server test, owner, and restricted evidence policy. A hidden UI control alone cannot prove server denial.

## Plan and AgentWorks tools

Access Review Analyst writes `access-review-matrix/v1` with one selected failed cell and its direct server attempt. Run the bundled `matrix` validator before Security Remediation Coordinator consumes it. Remediation re-reads current policy and issue state, records a reviewed change and deployment, then an independent retest of the same cell. Run the bundled `remediation` validator before reporting closure. Builder must insert these as blocking scripted steps and bind their exact input file paths; merely describing a validator is insufficient. No credential is copied between Crews.

## Knowledge and persistence

Save policy, scope, actor/fixture, cell, attempt, trace, build, issue, change, approval, deployment, retest and closure IDs with source times. Preserve failed attempts and earlier decisions. A policy, actor, resource, or build change requires a fresh matrix or explicitly new case; a later run cannot reuse a passing test from a different cell.

## Validation and reporting

The [validator](scripts/validate_handoff.py) checks exact identity, direct evidence, change/deployment joins, same-cell retest and independent closure. The [matrix](examples/access-review-matrix.json) and [pending ledger](examples/access-remediation-pending.json) pass. The [verified ledger](examples/access-remediation-verified.json) separates approval, deployment, retest and closure receipts. The [invalid ledger](examples/invalid-access-remediation.json) claims unsupported closure and fails. The reporting dashboard shows selected cell, source state, owner, deployment, retest, disposition and blockers; it never turns an unknown into a pass.

## Guardrails

Do not test outside written scope, use live customer identities without authorization, expose sensitive response bodies, grant privileges, waive a policy cell, create an issue, merge, deploy or close through installation. A test response on an old build is not remediation evidence. Redact traces and retain only approved source references.

## Read details when needed

- [Team and handoffs](references/team-and-handoffs.md) defines the exact route and repeat rule.
- [Setup](SETUP.json) has ten chat checks and leaves progress pending.
- [Workflow design and outcomes](../../references/workflow-design-and-outcomes.md) defines shared planning and reporting rules.

## Completion contract

Return Crew IDs, written scope, policy cell, validated matrix and ledger paths, source and owner decisions, retest or blocker, manual run, and activation choice. Leave recurrence off until reviewed.
