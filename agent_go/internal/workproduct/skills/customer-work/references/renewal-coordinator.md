# Renewal Coordinator

This skill provides the Renewal Coordinator capability. It can seed one Crew or be added to an existing Crew without changing that Crew's primary identity.

## First useful result

1. Bind the exact account, tenant, contract, subscription, entity, owner and review cutoff. Re-read executed terms and revisions; resolve auto-renewal, notice deadline and effective renewal date.
2. Read a validated bounded health brief and current billing state. Separate observed first-value, usage, support and payment facts from relationship hypotheses or missing coverage.
3. Calculate days to notice under the agreed timezone. Flag a passed deadline without inventing whether notice was sent or whether the contract renewed.
4. Prepare investigate, customer-discussion review, terms review or no-action options with a stable case key and due rule. A recommendation is not an accepted owner decision.
5. Present contract facts and uncertainty to the renewal owner. Any customer message, CRM change or term amendment needs a separate approved exact-object route and provider receipt.

Deliver **An exact-contract renewal register with current terms, notice timing, source coverage, owner options, decision state and next verification.** Cite the exact executed agreement and revision, subscription and billing observation, notice rule, health artifact and current source timestamps. Keep contract facts apart from health hypotheses.

## Renewal decision and repeat rule

Retain contract revision, renewal case key and prior owner decisions. Re-read terms, notice receipts, invoice state and health evidence before each review. A changed contract or auto-renewal term requires a new decision; a health signal cannot override executed terms. Keep any outreach or amendment in a separate approved route.

## Fictional worked example

Fictional input: tenant-demo/account-acme has executed contract C-42 revision r3, renewal 2026-12-01 and a 60-day notice deadline of 2026-10-02 in UTC. A current billing export on 2026-09-26 shows one open September invoice. Validated health brief H-42 says first value reached, with one unresolved training case; its renewal effect is unknown. No notice receipt exists.

Reviewable output: register R-42 binds C-42/r3 and the same account, calculates six days to notice at the 2026-09-26 cutoff, labels the invoice open rather than unpaid cash loss, and proposes owner review of the training case, billing status and contract options by 2026-09-28. Decision=pending; notice_sent=false; amendment=none; customer contact=none. It does not predict churn or claim the contract will renew.

## Inadequate output to reject

“This customer will churn because an invoice is open. Send a discount and cancel auto-renewal now.” Reject: an open invoice and training case do not prove intent, and neither a discount nor a contract change was reviewed or receipted.

This example does not complete setup. Reproduce one case from authorized customer records and save the owner's review.

## Automation handoff

Renewal Risk to Owned Decision consumes validated renewal-health-brief/v1 and emits renewal-decision-register/v1 for the same tenant/account, with a current contract and subscription read, notice calculation and owner state. The route validates both artifacts before reporting. The Crew step must supply schema fields and an output path; ask Builder to repair a route that omits them. A validator checks shape and references; a human checks source truth.

## Boundaries

Do not infer churn probability, promise a renewal, change contract terms, issue credits, mark an invoice paid, send notice or contact the customer from installation or a read-only review. Never copy another Crew's credentials. Installing this skill enables no schedule, trigger, function, Automation, customer message, CRM write, or account action.
