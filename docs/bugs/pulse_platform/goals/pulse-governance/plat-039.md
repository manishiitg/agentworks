[← goals / pulse-governance](index.md)

# PLAT-039 — advisor recommendations fall into the engineering-fix queue

| Field | Value |
|---|---|
| State | open |
| Priority | P1 |
| Product | goals |
| Area | pulse-governance |
| Summary | Pulse advisor artifact/lifecycle contract |

| Coordination | Value |
|---|---|
| Assigned agent | `Codex` |
| Ticket state | `runtime_reverify` |
| Last synchronized | `2026-08-06` |

- **Priority:** P1
- **Owner:** Pulse advisor artifact/lifecycle contract
- **Source workflow:** Hetzner SSH
- **Source evidence:** three Strategy Auditor findings were stored as raw
  `filed` concerns with no lifecycle disposition or structured route, so the
  generic UI labeled them **New** and said Pulse needed to diagnose and repair
  them.

## Problem

Strategy Auditor and Goal Advisor already described a `recommended_route` in
their human-readable review packets, but `PulseFindingDetails` did not retain
that field and the backend did not require it beside each `CONCERNS:` marker.
Consequently, a product recommendation, a future-evidence hypothesis, and a
bounded engineering handoff were indistinguishable after persistence. The UI's
ordinary open-finding fallback then treated all three as engineering defects.

## Fix

- Persist `recommended_route` and `next_check` in structured finding details.
- Require every Strategy Auditor and Goal Advisor concern to carry exactly one
  matching `PULSE_FINDING_JSON` route marker.
- Reject `evidence_wait` without an exact next check and reject a
  `recommended_route=none` conclusion that is nevertheless filed as a concern.
- Enforce route-compatible lifecycle dispositions: decisions must become
  `awaiting_user`, evidence waits must become `proposal_only` with the same
  next check, and a Fixer handoff cannot be parked as a proposal or decision.
- Project legacy route-less advisor records as **Untriaged recommendation** in
  **Ideas**, not **Pulse to fix**. Only `fixer_handoff` uses the engineering
  action queue.

## Verification

Focused and broader checks completed on 2026-08-06:

```text
go test ./pkg/orchestrator/agents/workflow/step_based_workflow \
  -run 'TestAdvisorConcernsRequireDurableRouting|TestAdvisorProposalRoutingRequiresEvidenceOrDecision|TestAdvisorAwaitingUserRequiresOwnedDecision|TestRecordPulseReviewQuarantinesAdvisorConcernWithoutRoute'
npx vitest run src/components/workflow/pulseFindingPresentation.test.ts
npx tsc -b
```

The complete `step_based_workflow`, `cmd/server/guidance`, and `cmd/server`
test invocation reached two pre-existing prompt-surface failures unrelated to
this ticket (`logical HTTP-backed tools` and `run_goal_advisor_review`); the
guidance and server packages passed, as did all PLAT-039-focused tests.

## Runtime acceptance

On the next Strategy Auditor or Goal Advisor run:

1. every trackable finding has one durable route;
2. a decision produces a linked decision card;
3. an evidence wait appears under Ideas with its next evidence boundary;
4. only a technical `fixer_handoff` appears under Pulse to fix; and
5. a malformed advisor review is retained as `contract_failed` without filing
   raw concerns.
