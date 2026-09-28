# Dispute to Reconciled Outcome team and handoffs

This package proposes a Workflow through Builder chat. Installing it does not connect a processor, submit evidence, contact a customer, post to an accounting ledger or start a schedule.

## Route and state transitions

1. **Review the exact case.** Billing Operations Coordinator with the Dispute Review pack reads the current dispute, linked payment, provider account and mode, case revision, deadline with timezone, prior submissions and reason-relevant authorized evidence. It emits `dispute-case/v1`. Missing evidence stays visible; a draft packet is not a submission.
2. **Gate the handoff.** Save JSON as the producer Crew's `context_output`, require its load-bearing fields with `update_validation_schema`, then execute `scripts/validate_handoff.py` before the Finance Crew runs. The first gate catches malformed output; the business gate checks exact identity and state rules.
3. **Separate owner action.** For any response, an authorized owner approves the exact packet and submission route. Re-read the current case and existing submissions. Use a stable case/revision/packet idempotency key. Record a provider submission receipt only if the processor confirms it. A timeout or uncertain response requires lookup, not blind retry.
4. **Observe provider outcome.** A later provider record can show under review, won or lost. Preserve the raw status, revision and observed time. A packet accepted for review is not a win.
5. **Review finance.** Revenue & Close Analyst consumes the validated case, reads settlement and ledger state for the same entity, dispute, payment and currency, and emits `dispute-finance-review/v1`. Reconciled requires matching principal treatment and a fee treatment decision backed by records. A won or lost provider outcome alone is not a journal match.

The included fictional case is `needs_response` with one missing usage source. Finance correctly reports `pending_response`. The [submitted case](../examples/dispute-case-submitted.json) retains an exact packet receipt while [Finance awaits](../examples/dispute-finance-awaiting.json) a provider decision. A later [observed loss](../examples/dispute-case-closed-lost.json) can reach [reconciliation](../examples/dispute-finance-reconciled.json) only after the matching principal and fee decision evidence is read.

## Identity, freshness and duplicate rules

Join on `entity_id + provider_account_id + provider_mode + dispute_id + payment_id + customer_id + currency`; compare amount in minor units. The provider case revision and observed time must be saved, and the deadline must be read from that case rather than calculated from a generic rule. Names, invoice text and matching amounts alone are not stable joins. A changed case revision, response deadline, provider status or submission history invalidates an old action proposal.

Before any submission, inspect the current provider record and prior receipt for the exact packet hash. Do not submit automatically from the case artifact. If provider submission is uncertain, hold until the source is checked. Keep the original case packet and any later state update as separate versions with source IDs and Crew run IDs.

## Local contract exercise

Run the package suite with `python3 scripts/test_validate_handoff.py`. For a specific handoff, use `python3 scripts/validate_handoff.py <case.json> <finance.json>` with paths to actual run artifacts. The bundled fixtures are fictional and passing them does not establish live access, source truth, owner approval or a finance reconciliation.
