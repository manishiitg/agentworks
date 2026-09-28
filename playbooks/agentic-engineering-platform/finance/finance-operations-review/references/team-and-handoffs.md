# Finance Operations Review: team and handoffs

## Required team

| Slot | Crew template | Produces | Access |
| --- | --- | --- | --- |
| Billing | `billing-operations-coordinator` | `billing-exception-queue/v1` | Authorized subscription, invoice, payment, refund, and dispute records or exports |
| Finance | `finance-analyst` | `finance-impact-readout/v1` | Validated billing queue plus authorized ledger or finance export |

Reuse a ready Crew when its owner, selected skill, setup evidence, and source access match. Otherwise propose a new Crew through Builder chat. Keep distinct Crew IDs for the two required slots. Optional Revenue & Close Analyst and Spend & Payables Coordinator join only after their source and approval boundaries are reviewed. Their output contracts are proposals until a separate validator is supplied; do not route them as automated handoffs in the first version.

## First route

1. Billing Crew reads one bounded period and emits a queue with stable case IDs, source IDs, current statuses, amount in minor units, and a proposed action. A proposed refund is never marked executed.
2. A script step runs `python3 skills/agentworks-playbook-finance-operations-review/scripts/validate_finance_artifact.py queue path/to/billing-exception-queue.json` from the Workflow workspace. Use the actual installed skill path if renamed. Stop the route if it fails.
3. Finance Crew receives the validated queue, source references, and its own approved finance records. It emits a readout with the same entity, period, and currency, citing case IDs and calculation inputs.
4. A script step runs the same installed validator with `readout path/to/finance-impact-readout.json --queue path/to/billing-exception-queue.json`. Stop the route if it fails.
5. The owner reviews the readout and exact proposed next actions. Record a manual test run, including validator results, before any recurrence.

Only pass the fields required for this route. No copied credentials, broad customer exports, or implied permission to perform writes. The validators check shape, arithmetic, IDs, and handoff scope; a human must verify the underlying source records and business interpretation.

## Artifact contract

The JSON examples in `../examples/` are fictional contract samples. Each source reference has a stable ID, a URI or export row reference, and an observation time. Case IDs are unique within a queue. A readout must cite its queue ID and may only cite case IDs present in that queue. Refund cases state original, previously refunded, proposed, and remaining minor-unit amounts; the validator rejects an over-refund. Metrics use signed integer minor units for amounts that can be negative, distinguish invoiced, collected, and recognized amounts, and name their source references and formula. Unknown or unavailable values remain explicit limitations rather than zeroes.
