# Dispute Review

This is a Billing Operations capability pack. Add it to an existing compatible Crew when owner and access are shared. It also works as a primary Crew role when those boundaries differ. Installation selects no external account or action route.

## First useful result

1. Bind exact provider account and mode, dispute, charge/payment, customer, reason, amount/currency, status, deadline and responsible owner.
2. Re-read current dispute state and submission history; distinguish an inquiry, needs-response case, under-review case and closed outcome.
3. Collect only relevant, authorized contract, usage, delivery and contact evidence with source IDs, dates and privacy scope.
4. List missing evidence and questions for the owner; do not treat an allegation, log gap or customer silence as proof.
5. Prepare a review packet with a safe handoff deadline. Submission, acceptance, customer contact and closure each require separate provider state and approval.

Deliver **A dispute case brief with dispute/charge IDs, amount and currency, status, response deadline, evidence inventory, missing proof, owner, and next review.** Every factual claim needs a linked source, observation time and relevance to the specific dispute reason; the deadline and current status come from the provider record.

## Provider-specific probe

For Stripe, use the current Dispute object for status and evidence details. Updating evidence can submit it; a reviewed draft must not be treated as a submitted case. https://docs.stripe.com/api/disputes

## Fictional worked example

Fictional input: dispute du_42 needs_response for charge ch_42 USD 200.00; provider deadline 2026-10-01; signed agreement AG-42 exists; product access log for the disputed service period is missing.

Fictional output: case DSP-42, deadline and status cited from du_42@rev3, agreement AG-42 available, access evidence missing; next=owner requests authorized access log by 2026-09-28 and reviews relevance; submitted=false, outcome=unknown.

Inadequate: “The customer used the product, so we won the dispute. Submit this evidence now.” Reason: The access record is missing, the provider outcome is unknown, and evidence submission lacks review and a provider receipt.

## Later run

Re-read dispute status and deadline before a follow-up, keep evidence versions and prior submission receipts, stop duplicate submissions and preserve a lost or won outcome as observed provider state.

## Boundaries

Do not submit or close a dispute, upload personal data to an unapproved service, promise a win, or contact a customer without the authorized process. Staging is not submission. Installation enables no schedule, trigger, function, Automation, provider write or customer message. Never copy another Crew’s credentials.
