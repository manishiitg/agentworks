# Refund Review

This is a Billing Operations capability pack. Add it to an existing compatible Crew when owner and access are shared. It also works as a primary Crew role when those boundaries differ. Installation selects no external account or action route.

## First useful result

1. Bind exact account and mode, customer, original payment or charge, request, amount in minor units, currency, reason, policy revision and approver.
2. Re-read payment capture/success state and every succeeded, pending or failed prior refund; separate provider state from a support request.
3. Calculate remaining refundable amount under provider and owner policy; block duplicate or overlapping requests and cross-currency ambiguity.
4. Compare the proposed amount and reason with policy, prior concessions and approval limits; label exceptions for a named reviewer.
5. Return a decision record and optional unsent reply. Actual refund needs a separate exact-object approval, state recheck, idempotency key and provider receipt.

Deliver **A refund decision record with payment/charge ID, original and prior-refunded amounts, remaining refundable amount, requested amount, currency, policy match, approver, and proposed state.** Show original, previously refunded, pending and requested amounts in the same minor-unit currency, all provider IDs, observed time, approval policy and remaining amount before any action.

## Provider-specific probe

Stripe supports partial refunds up to the remaining unrefunded amount of the charge; use a Charge or PaymentIntent identifier and an amount in the smallest currency unit. https://docs.stripe.com/api/refunds/create

## Fictional worked example

Fictional input: charge ch_88 succeeded for USD 100.00; prior succeeded refund re_1 USD 25.00; no pending refund; new request USD 50.00; policy allows partial refund with owner approval.

Fictional output: request RF-88, original=10,000 cents, prior refunded=2,500, remaining=7,500, requested=5,000, after-request remainder=2,500; currency USD; owner decision=pending; provider refund ID=none; customer draft=unsent.

Inadequate: “Refund 100 dollars; it has already been processed.” Reason: It ignores the prior refund and requested amount, exceeds the remaining balance, and asserts a provider action without a receipt.

## Later run

Re-read payment and refund objects immediately before any action, retain stable request and idempotency keys, avoid a second refund, and update customer state only from provider receipts.

## Boundaries

Do not issue a refund, promise its completion, change a payment, or send a message from installation. A reviewed proposal is not a processed refund. Installation enables no schedule, trigger, function, Automation, provider write or customer message. Never copy another Crew’s credentials.
