# Customer Onboarding Coordinator

This skill provides the Customer Onboarding Coordinator capability. It can seed one Crew or be added to an existing Crew without changing that Crew's primary identity.

## First useful result

1. Confirm the account, purchased product or plan, promised outcomes, first-value definition, onboarding owner, dates, and permitted handoff scope.
2. Read the authorized signed handoff and current onboarding tracker. Separate contractual facts from sales notes and assumptions; reconcile conflicting dates or scope.
3. Define a short sequence of observable milestones with a stable ID, accountable owner, due date, completion evidence, and blocker state for each.
4. Mark a milestone complete only from an authorized product, customer, or owner record. Treat an invitation or setup email as an invitation, not adoption.
5. Return the current register, missing inputs, at-risk dates, and one next decision for the owner to review.

Deliver **An onboarding milestone register with owners, evidence, blockers, and the next customer decision.** Every completion and blocker cites a source and observation time; unobserved milestones remain pending or unknown.

## Milestone quality

Use the customer's actual promised scope; do not import a generic SaaS checklist as fact. Stable milestone IDs let the next run report changes rather than duplicate tasks. If the sales handoff is incomplete, show the exact missing decision. Customer outreach is a separate approved action with contact policy and prior-message checks.

## Fictional worked example

Fictional input: signed handoff signed-handoff binds account-example-001 to tenant-example-001, names the onboarding owner and customer sponsor, and promises a production workspace plus a first project report export by 2026-09-30 17:00 UTC. Workspace record workspace-record shows the tenant provisioned at 09:45 on 2026-09-25. At the 10:00 review cutoff, no authorized export event is available.

Reviewable output: register register-example-001 for the exact account/tenant. Milestone “workspace-ready”: **complete**, owner Onboarding, due 2026-09-25 17:00, evidence workspace-record at 09:45. Milestone “first-report”: **pending**, owner Customer sponsor, due 2026-09-30 17:00, no completion evidence. First-value rule first-report-v1 is an authorized customer user exporting a project report from production. Next decision: account owner confirms the sponsor and whether training is needed; customer contact remains pending review. No invitation or workspace provision is called first value.

## Inadequate output to reject

“Onboarding is complete because the workspace invitation was sent.” Reject: an invitation is neither observed production provisioning nor the agreed customer report export, and the claimed milestone lacks a source.

This example does not complete setup. Reproduce one case from authorized customer records and save the owner's review.

## Automation handoff

For New Customer to First Value, emit plain JSON `onboarding-milestone-register/v1` at the path supplied by the Crew step. Include account and handoff IDs, first-value goal, milestone state, source references, and owner. The Workflow validates it before Adoption consumes it. The Crew step must supply schema fields and an output path; ask Builder to repair a route that omits them. A validator checks shape and references; a human checks source truth.

## Boundaries

Do not promise implementation dates, provision access, change account settings, or contact the customer without an authorized route and exact owner decision. Never copy another Crew's credentials. Installing this skill enables no schedule, trigger, function, Automation, customer message, CRM write, or account action.
