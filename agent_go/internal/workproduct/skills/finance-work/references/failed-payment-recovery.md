# Failed Payment Recovery

This is a Billing Operations capability pack. Add it to an existing compatible Crew when owner and access are shared. It also works as a primary Crew role when those boundaries differ. Installation selects no external account or action route.

## First useful result

1. Bind exact provider account, mode, customer, subscription, invoice, payment attempt, currency, policy revision, observed time and owner.
2. Re-read current invoice and payment state, failure code, retryability, next automatic attempt, updated payment method and prior contact.
3. Separate retry scheduled, retry exhausted, already recovered, customer action needed and unknown; avoid interpreting one event as current state.
4. Apply the owner’s retry and consent policy, considering the provider’s automated dunning and prior customer messages.
5. Return one policy-safe next action and an unsent draft when contact is permitted; a charge retry or message is a separate approved route.

Deliver **A failed-payment case with invoice/payment/subscription IDs, failure and retry state, amount, customer contact history, policy next step, owner, and unsent draft if appropriate.** Cite the latest invoice/payment attempt, failure and next-attempt source, time, amount/currency, prior contact and policy decision. A missing next attempt must not be described as scheduled.

## Provider-specific probe

For Stripe, inspect invoice status, amount_remaining, attempt_count and next_payment_attempt on the current invoice; automatic retries and dunning may already be configured. https://docs.stripe.com/api/invoices/object

## Fictional worked example

Fictional input: subscription sub_9, invoice in_9 open for USD 120.00; attempt att_3 failed 2026-09-25; next automatic attempt=2026-09-28; provider dunning email already queued; cutoff=2026-09-26.

Fictional output: recovery case REC-9, status=automatic retry pending, source in_9@rev4 and att_3; owner action=verify whether the queued provider email was delivered after the retry; manual send=hold; amount remaining USD 120.00; no charge or contact receipt exists.

Inadequate: “The card failed; retry it now and send an email.” Reason: It ignores the provider retry and dunning schedule, does not verify current invoice state, and proposes unapproved charge and contact actions.

## Later run

Deduplicate events by provider event and invoice IDs, re-read next_payment_attempt and invoice status, stop after recovery or policy exhaustion, and avoid duplicate messages during automatic dunning.

## Automation handoff

In Subscription Receivable to Verified Outcome, return only receivable-review/v1 JSON for one exact invoice. Include the current failed/pending attempt and next provider retry with sources, current balance, prior contact and suppression. Do not turn a scheduled retry into a successful collection or propose duplicate customer contact.

## Boundaries

Do not trigger a charge, change a payment method, cancel a subscription, or contact a customer without exact approval and provider evidence. Never request card data in chat. Installation enables no schedule, trigger, function, Automation, provider write or customer message. Never copy another Crew’s credentials.
