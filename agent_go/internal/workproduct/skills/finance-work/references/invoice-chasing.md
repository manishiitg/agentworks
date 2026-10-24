# Invoice Chasing

This is a Billing Operations capability pack. Add it to an existing compatible Crew when owner and access are shared. It also works as a primary Crew role when those boundaries differ. Installation selects no external account or action route.

## First useful result

1. Bind exact business, provider account and mode, invoice, customer, currency, due-date policy, cutoff, owner and contact channel.
2. Re-read current invoice status, amount remaining, credits, partial payments, collection method, payment attempts and contact history.
3. Exclude draft, paid, void, disputed or owner-suppressed cases; identify an active automatic reminder or retry before suggesting another message.
4. Rank actionable open balances by due date and policy priority; retain a stable case and contact action key.
5. Prepare a factual unsent message with exact recipient, amount, invoice link if approved, and owner decision; delivery is separate.

Deliver **A ranked overdue-invoice queue with invoice/customer IDs, open amount, due and last-payment dates, prior or scheduled contact, owner, and unsent next message.** A follow-up row needs current invoice/payment source IDs, observed time, amount remaining in minor units and currency, prior-contact source, policy rule, and reason it is due now.

## Provider-specific probe

For Stripe, verify the invoice status and amount_remaining from the exact invoice object; draft, open, paid, uncollectible and void are distinct states. https://docs.stripe.com/api/invoices/object

## Fictional worked example

Fictional input: invoice in_42 open, USD 500.00 due 2026-09-20, amount_remaining=USD 300.00 after a partial payment; provider reminder queued for 2026-09-27; cutoff=2026-09-26; owner=AR lead.

Fictional output: case AR-42, source invoice in_42@rev5 and payment p_8; open balance USD 300.00, six days past due at cutoff; automatic reminder scheduled tomorrow, so manual send=hold; next=AR lead review after provider attempt. No message was sent.

Inadequate: “Invoice 42 is unpaid for 500 dollars. Email the customer now.” Reason: It ignores the partial payment and scheduled reminder, lacks exact customer and approval, and falsely implies a send.

## Later run

Re-read current payment and prior-contact state, deduplicate by invoice and contact action ID, respect quiet periods and provider dunning, and close or defer resolved cases.

## Automation handoff

In Subscription Receivable to Verified Outcome, return only receivable-review/v1 JSON for one exact invoice. Preserve entity, provider account/mode, customer, cutoff, total, credits, prior successful collections, remaining amount, contact suppression, approval and delivery state. A draft is unsent until an approved exact action has a provider receipt.

## Boundaries

Do not send a reminder, change terms, mark an invoice paid or uncollectible, or initiate a charge through installation. A send needs exact-recipient approval, current state recheck and provider receipt. Installation enables no schedule, trigger, function, Automation, provider write or customer message. Never copy another Crew’s credentials.
