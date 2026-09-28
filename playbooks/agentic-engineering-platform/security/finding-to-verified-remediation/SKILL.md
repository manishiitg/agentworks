---
name: finding-to-verified-remediation
description: Propose an authorized security finding and remediation handoff with exact deployment and independent retest evidence.
---

# Finding to Verified Remediation

## Outcome

Produce a validated security finding and an owned remediation ledger. A proposed fix, merged change, deployed build, passing retest, accepted risk, and closed finding are different states.

## When to use

Use for one finding within written asset and method scope when assessment and remediation need accountable owners. A read-only export can support first triage. An Access Review Analyst may supply separately validated permission evidence, but is not an automatic slot in this route.

## Discovery and user direction

Inspect existing Security Crews, scope authorization, current finding and asset state, severity policy, issue, change, deployment, retest sources, and closure authority. Propose reuse or reviewed creation. Show the exact route before Builder applies it; selection leaves setup pending.

## Required inputs

Bind tenant, finding ID, asset, affected environment and build, scope authorization, allowed methods, source report, severity rule, owner, fix criterion, independent retest rule, and closure policy.

## Plan and AgentWorks tools

Reuse Security Findings Analyst and Security Remediation Coordinator. Findings writes `security-finding/v1` with applicability and source evidence. Run the bundled validator before Remediation consumes it. Remediation re-reads current source state and writes `security-remediation-ledger/v1` with issue, reviewed change, deployment, retest, and disposition. Add explicit validator steps in the Workflow. Require separate authorization for any ticket, code, deployment, scan, or closure write.

## Knowledge and persistence

Save scope, tenant, finding, asset, environment, observed and deployed build, policy, issue, change, approval, deployment, retest, disposition, run, and action IDs. Re-read current finding and deployment before repeats; retain failed retests and earlier risk decisions.

## Validation and reporting

The [validator](scripts/validate_handoff.py) checks exact joins, scope, applicability, evidence, deployment, retest identity, and closure gates. Fictional [finding](examples/security-finding.json) and [remediation ledger](examples/security-remediation-ledger.json) pass in their open state; the [merged-only closure](examples/invalid-security-remediation-ledger.json) fails. A separate [verified ledger](examples/verified-security-remediation-ledger.json) shows the evidence required for closure. The reporting dashboard distinguishes unconfirmed, owned, change pending, deployed, retest blocked, verified, accepted risk, and closed states with restricted source references and cost.

## Guardrails

Do not test outside written scope or run intrusive probes through installation. Do not expose restricted evidence, merge, deploy, accept risk, or close a finding without the proper authority. A scanner result alone does not establish applicability; a merged fix alone does not establish deployed remediation.

## Read details when needed

- [Workflow design and outcomes](../../references/workflow-design-and-outcomes.md) for route decisions.
- [Team and handoffs](references/team-and-handoffs.md) for evidence gates and [setup](SETUP.json) for ten checks.

## Completion contract

Return Crew IDs, written scope, source map, validated artifacts, manual case, owner decisions, deployment and retest state, disposition, activation choice, and blockers. Keep recurrence off until reviewed.
