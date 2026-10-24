# Lead Intake & Qualifier

This skill provides the Lead Intake & Qualifier capability. It can seed one Crew or be added to an existing Crew without overwriting that Crew's primary identity.

## First useful result

1. Confirm the company, offer, target market, qualification criteria, lead source, owner, and current reporting window.
2. Read only authorized form, inbox, or CRM records. Preserve source and observed time; check for duplicate contacts, companies, and open opportunities before creating a new case.
3. Separate prospect-provided facts, verified account facts, and inferences. Assess fit criterion by criterion and mark unknown information explicitly.
4. Record inbound intent, urgency only when evidenced, current stage, contact policy status, and the exact owner decision needed.
5. Produce one stable brief per lead with a qualified, needs-review, or not-fit recommendation and a reason. Ask the owner to review a representative result before marking setup complete.

Deliver **A lead qualification brief with fit reasons, source references, missing details, and a proposed owner.** Each brief cites the original enquiry or export row, qualification policy, checked duplicate scope, and the source of every fit claim.

## Qualification decisions

Use the owner's actual ideal-customer profile and disqualification rules. A missing company size, budget, timing, or authority signal is **unknown**, not a negative score. Do not invent an intent score or convert a content download into a demo request. A duplicate CRM record should be linked for owner review, not silently merged or overwritten. For a not-fit or contact-blocked lead, return an internal disposition and stop the outbound handoff.

On repeat intake, use the lead source ID and stable brief ID to check whether the record, owner, or stage changed. Report the delta and avoid opening a second case for the same enquiry.

## Fictional worked example

Fictional input: enquiry lead-example-001 in exports/fictional-inbound-leads.csv row 7 asks for a demo of multi-location appointment scheduling. Policy docs/fictional-ideal-customer-profile.md section 1 includes clinics in the supported market. The authorized open-lead export query reports no duplicate as of 09:40 UTC. The enquiry gives no budget and contact policy still needs review.

First result: brief-example-001 identifies the source row and policy, marks market **match** and budget **unknown**, records duplicate_state **none** within the checked export, and recommends **qualified** for owner review. The next action is to review a reply draft and contact policy. No CRM record or message is created. On a later run, recheck the source ID, duplicate scope and policy before changing the brief.

## Inadequate output to reject

“This clinic is a high-budget hot lead, so add it to the CRM and start a sequence.” Reject: the budget is unknown, the intent score is invented, contact policy is unresolved, and neither the CRM write nor outbound sequence is authorized.

The fictional example does not complete setup. Reproduce one case from authorized customer records and save the owner's review.

## Automation handoff

For an approved Inbound Lead-to-Meeting Review route, emit plain JSON `lead-qualification-brief/v1` at the path supplied by the Crew step. Include source IDs, fit evidence, duplicate state, contact policy state, and a stable brief ID. The Workflow validates it before Follow-up consumes it. The Crew step must supply the schema fields and output path; ask Builder to repair a route that omits them. A validator checks structure and references, while a human verifies source truth.

## Boundaries

Do not infer consent to email from a form submission, scrape private contacts, create a CRM record, or contact a lead without an authorized route and policy. Never copy another Crew's connection or credentials. Installing this skill does not enable a schedule, trigger, function, Automation, outbound message, or account write.
