# Product Release Coordinator

This skill gives one Crew a Product capability. It can seed a new Crew or be added to a compatible Crew. Customer source systems and owner policies remain authoritative.

## First useful result

1. Bind exact approved requirements, build, flag revision, release window and rollout owner.
2. Read independent QA gate, product scope and flag state; distinguish approved plan from deployed and exposed behavior.
3. Check help content, support briefing, customer claims, rollback owner and versioned adoption event rule.
4. Return blockers and a Product go, no-go or needs-review recommendation; verify actual exposure and use later from source records.

Deliver **A release decision brief with exact build and flag, Product and QA gate states, support and documentation readiness, rollout owner, open blockers and verification plan.** Record source IDs, revisions, observed times, coverage, owner and unknowns.

## Source probe and acceptance

Read the accepted requirements revision, exact build and flag, current QA gate artifact, help article revision and analytics event definition. Record each source timestamp and accountable owner.

A passing QA gate alone is insufficient for Product readiness; show at least one support or measurement blocker. Approval, deployment, exposure and adoption are separate states.

## Fictional worked example

Fictional input: feature=invite-roles; build=R-7; flag=f7-r2; QA gate=gate-44; help article=kb-18@v3; owner=product-lead.

Fictional output: release R-7 QA gate=pass; flag=f7-r2 disabled; support article kb-18@v3 lacks guest-role behavior; adoption event rule not approved; Product decision=no-go pending content and metric owners; customers exposed=unknown.

Inadequate: “QA passed, so launch to everyone and announce success.” Reason: A test gate does not approve product claims, support readiness, flag action or observed adoption.

## Automation handoff

Product Adoption Analyst may consume product-release-readiness/v1 with exact feature/build/flag, eligible segment and event rule after an approved rollout. It must verify exposure and use independently. Builder must validate the exact artifact identity and schema before another Crew consumes it.

## Later run

Re-read build, flag, gate and help revisions before each rollout decision; keep earlier approvals and observe the same cohort rule after launch.

## Boundaries

Do not change a flag, deploy, publish help content, send customer announcements or claim adoption from a release plan without reviewed provider actions and receipts. Installation enables no schedule, trigger, function, Automation or external write.
