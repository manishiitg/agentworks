# Refund decision and reconciliation handoff

## Journey

1. Builder inspects existing Billing Operations and Revenue & Close Crews and proposes reuse or creation. Put the Refund Review pack on Billing Operations and verify its own pending checklist.
2. Billing re-reads the exact customer request, payment and all relevant refunds. It computes `remaining_before_minor = original_minor - succeeded_refunds_minor - pending_refunds_minor` and checks whether the requested amount fits. Pending funds are reserved for this decision; the provider's own refundable amount remains authoritative before a write.
3. Validate `refund-decision/v1`. The finance Crew receives only the bounded artifact and authorized references. A proposed or approved refund still has `provider_refund_id = null` and `provider_receipt = null`.
4. If the owner separately authorizes action, re-read current payment/refunds, verify amount and currency, use a stable idempotency key, and save the provider result. Customer contact and ledger posting are separate decisions.
5. Finance re-reads provider and ledger state. Emit `refund-reconciliation/v1` as `pending_action`, `denied`, `blocked`, `processed_unreconciled`, or `reconciled`. `reconciled` needs a succeeded provider refund and matching ledger entry or bank evidence under the owner's accounting policy.
6. A later run compares source revisions and receipts, tracks unresolved differences, and avoids a second refund or a false completion claim.

## Contract

Both artifacts must match entity, account, mode, customer, request, payment and currency. Billing's amount calculation, owner decision, action status and source IDs must be internally consistent. Finance must cite that exact decision artifact and current provider and ledger observations. `processed_unreconciled` needs a provider receipt; `reconciled` additionally needs a matched ledger reference. The bundled validator checks these mechanical joins and state claims. It cannot decide the customer's policy or accounting treatment; named reviewers must do that on real records.

## First manual case

Run on one real authorized request with both Crews and blocking validation. Save owner corrections and the action ledger. A manual case may legitimately stop at `pending_action` with no provider write; its outcome is a reviewed decision and a known next step, not a claim that the refund was processed. The fictional fixtures only teach the contract.
