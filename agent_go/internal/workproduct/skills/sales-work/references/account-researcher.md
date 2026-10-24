# Account Researcher

This skill provides the Account Researcher capability. It can seed one Crew or be added to an existing Crew without overwriting that Crew's primary identity.

## First useful result

1. Resolve the legal or trading company and canonical domain; flag ambiguous matches rather than blending companies.
2. Inspect the company site and approved public or customer-provided sources within a bounded date range.
3. Capture product, audience, geography, recent changes, and possible relevance to the seller’s offer with direct source references and observation times.
4. Label each claim observed, owner-provided, or hypothesis. Explain why a finding matters and what the seller should verify in conversation.
5. Produce a concise brief with no invented budget, technology stack, buying committee, or purchase intent.

Deliver **A dated account research brief with verified facts, relevant hypotheses, and questions for the seller.** Every factual finding has a stable ID, source URL or export reference, observation time, and evidence kind.

## Research quality

Prefer the account's own current site and the customer's approved CRM over unsourced directory summaries. Check a company-domain match before attaching a finding to a lead. Do not turn a job posting, funding announcement, or technology guess into a claimed purchase need. If the public site is sparse, return a short brief with explicit unknowns and discovery questions. Recheck time-sensitive facts before a later run.

## Fictional worked example

Fictional input: validated brief-example-001 identifies harborclinic.example. Its fictional /locations page, observed at 09:50 UTC, lists three clinic locations. The original demo enquiry asks about multi-location scheduling; it does not describe the current scheduling system or budget.

First result: research-example-001 links to brief-example-001 and the matching domain. “The site lists three locations” is **observed** with the page reference. “Coordinating those locations may matter” is a **hypothesis**, supported by the page and enquiry but explicitly unconfirmed. The seller's question is “How are appointments coordinated across the locations today?” Refresh the page before using this finding on a later date.

## Inadequate output to reject

“Harbor Clinic has three locations, uses a legacy scheduler, and is ready to buy our platform.” Reject: the scheduler and buying intent are unsupported, and a public page does not establish permission to contact anyone.

The fictional example does not complete setup. Reproduce one case from authorized customer records and save the owner's review.

## Automation handoff

If included in an Inbound Lead-to-Meeting Review, return plain JSON `account-research-brief/v1` linked to the validated qualification brief. The Workflow validates company identity and source references before Follow-up uses it. The Crew step must supply the schema fields and output path; ask Builder to repair a route that omits them. A validator checks structure and references, while a human verifies source truth.

## Boundaries

Do not scrape personal contact details, claim a company is buying from a weak signal, or send outreach. Public research is context, not permission to contact. Never copy another Crew's connection or credentials. Installing this skill does not enable a schedule, trigger, function, Automation, outbound message, or account write.
