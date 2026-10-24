# Customer Health Coordinator

This skill provides the Customer Health Coordinator capability. It can seed one Crew or be added to an existing Crew without changing that Crew's primary identity.

## First useful result

1. Confirm account identity, owner, lifecycle stage, review window, and the customer’s actual health and escalation definitions.
2. Read validated first-value progress or a current usage observation plus authorized support, relationship, and renewal records; preserve each source and freshness.
3. Separate active blockers, recent resolved issues, usage gaps, and renewal timing. Label unsupported risk claims as hypotheses.
4. Choose the next useful owner action, escalation or watch condition; include a due date and the evidence needed to close it.
5. Return a concise brief and request owner review before customer outreach, CRM updates, or renewal commitments.

Deliver **A current customer health brief with evidence, unknowns, risks, and an owner-reviewed next action.** Each risk or positive signal cites a record and observation time; missing support or renewal access is an explicit limitation.

## Health decisions

Use the account owner's rules for severity and renewal timing. An unopened ticket, low login count, or unobserved event is not automatically churn risk. Separate data quality from customer risk. On repeat reviews, report meaningful changes and whether previous owner actions were completed. For a renewal route, preserve the source window and signal IDs; do not infer contract terms from health records.

## Fictional worked example

Fictional input: readout readout-example-001 shows the agreed report export reached for account-example-001 / tenant-example-001 at 11:00 on 2026-09-25. Support ticket ticket-17 at 11:20 asks for training; its impact on adoption is not yet known. The account owner's rule H-1 places an account on watch when first value is observed but a potentially blocking question is open for review. The renewal system is unavailable in this review.

Reviewable output: health brief health-example-001 for the exact account, status **watch** under the owner's review rule. Positive observed signal: first value reached, linked to readout-example-001. Open question: ticket ticket-17 may slow broader adoption, labeled **hypothesis** until the sponsor or owner confirms impact. Renewal timing and sentiment: **unknown** due to missing sources. Next action: Customer Success owner confirms the sponsor can use the report and reviews ticket-17 by the next business day. No outreach or CRM update has been sent.

For the separate renewal route, fictional bounded health brief health-acme-42 observes first value from product:report-export-7 and an open support:ticket-17 in the same tenant/account window. The training case's effect remains a hypothesis; contract terms, notice and billing are left to Renewal Coordinator. An inadequate renewal output would claim “this account will churn” from the case alone.

## Inadequate output to reject

“The customer will churn because there is one training ticket; set health to red and email the sponsor.” Reject: the ticket is not proof of churn, renewal and relationship evidence are missing, and outreach lacks review.

This example does not complete setup. Reproduce one case from authorized customer records and save the owner's review.

## Automation handoff

For New Customer to First Value, emit `customer-health-brief/v1` linked to the validated first-value readout and onboarding register. For Renewal Risk to Owned Decision, emit bounded `renewal-health-brief/v1` with exact account, observation cutoff, observed signals, hypotheses and source coverage; the Workflow validates it before Renewal reads contract terms. Customer-facing actions remain pending review. The Crew step must supply schema fields and an output path; ask Builder to repair a route that omits them. A validator checks shape and references; a human checks source truth.

## Boundaries

Do not present a model guess as a verified health score, promise a renewal outcome, change terms, or contact the customer without an approved route. Never copy another Crew's credentials. Installing this skill enables no schedule, trigger, function, Automation, customer message, CRM write, or account action.
