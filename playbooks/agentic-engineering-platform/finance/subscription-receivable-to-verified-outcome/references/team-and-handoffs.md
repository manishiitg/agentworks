# Subscription receivable team and handoffs

## Bind the exact case

Reuse two distinct, authorized Crews: Billing Operations Coordinator with **Invoice Chasing** or **Failed Payment Recovery**, and Finance Analyst. Billing owns contact/retry review; Finance verifies monetary state from current records. A provider name or installed MCP alone is not source access. Probe one exact invoice, customer, provider account, live/test mode, currency and cutoff. Authorized exports can support a first read-only run.

## Billing artifact

Billing emits one `receivable-review/v1` per invoice. Preserve a stable case ID and exact provider invoice ID, subscription ID or `null`, customer, entity, account/mode, due date, cutoff and source references. Recompute `remaining_minor = invoice_total_minor - credits_minor - previously_collected_minor`; do not treat an attempted or pending payment as collected. Record latest failed attempt and provider retry time where present, prior/scheduled contacts, suppression, policy version, owner, proposed next step and next check. If a reminder was actually sent, its exact action key needs prior approval and provider delivery receipt; an unsent draft stays unsent. A disputed or suppressed case cannot claim a sent reminder.

The [fictional valid case](../examples/receivable-review.json) is an overdue USD 120 invoice with a failed attempt, scheduled provider retry and an unsent reviewed reminder. The [invalid case](../examples/invalid-receivable-review.json) fabricates a send and misstates the remaining balance.

## Finance artifact

Finance re-reads the invoice and payment status after the Billing cutoff. Its `receivable-outcome/v1` cites the exact Billing artifact ID, case, invoice, customer, entity, account/mode and currency. It records new successful collection separately from the balance at cutoff. `open` needs no new collection; `partially_collected` needs a provider receipt and a positive remaining balance; `collected_unsettled` needs a receipt covering the remaining balance. `verified_deposit` additionally needs a provider allocation tying this payment's gross, fee and net to a full payout, and separate bank and ledger records matching the full payout. It cannot equate this invoice's gross payment with a pooled or fee-reduced bank deposit. A pending retry, reminder delivery or `invoice.paid` event without an inspectable payment receipt cannot prove money moved. The [worked open outcome](../examples/receivable-outcome.json), [collected but unsettled outcome](../examples/collected-outcome.json) and [fictional verified deposit](../examples/deposited-outcome.json) demonstrate separate states; the [false deposit](../examples/invalid-receivable-outcome.json) fails.

## Manual Workflow route

1. Ask Billing to return only its JSON artifact. Save its run-file path, validate it, and inspect source truth.
2. Pass the bounded valid artifact and approved references to Finance. Ask for only its JSON artifact. Finance re-reads current provider and finance records, not a stale copied status.
3. Validate the outcome against the exact Billing file. If either check fails, keep the producer output for review and stop the next step. Show the owner the current state, action decision, blocked claims and next check date.
4. Save both Crew run IDs, artifact paths, validator outputs and the manual run. Keep recurrence paused until the owner reviews cadence, cost and source freshness.

```bash
python3 scripts/validate_handoff.py review examples/receivable-review.json
python3 scripts/validate_handoff.py outcome examples/receivable-outcome.json --review examples/receivable-review.json
```

The installed script path is `skills/agentworks-playbook-subscription-receivable-to-verified-outcome/scripts/validate_handoff.py`; use actual run files in a customer Workflow. The Crew runner saves responses but does not itself validate this contract. Builder must wire the blocking steps and test a failed case. If that cannot be done, leave the route manual and its setup check blocked. A reviewer still checks the underlying provider, contact and finance records; a syntactically valid receipt reference does not establish source truth.
