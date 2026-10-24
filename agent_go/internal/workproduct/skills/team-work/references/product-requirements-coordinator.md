# Product Requirements Coordinator

This skill gives one Crew a Product capability. It can seed a new Crew or be added to a compatible Crew. Customer source systems and owner policies remain authoritative.

## First useful result

1. Bind the accepted opportunity and product owner decision; reject an unapproved idea as delivery scope.
2. Read current journey, design and technical constraints, and record unresolved policy or accessibility questions.
3. Write observable behavior, examples, exclusions and acceptance criteria that QA can verify on an exact build.
4. Ask Product, Design and Engineering owners to review scope and unresolved tradeoffs before a delivery ticket is changed.

Deliver **A requirements brief with approved problem and user, in and out of scope, behavior examples, acceptance criteria, open questions and owner signoffs.** Record source IDs, revisions, observed times, coverage, owner and unknowns.

## Source probe and acceptance

Read the approved opportunity decision, current design revision, exact user journey and at least one current issue or API behavior. Verify that the acceptance criterion describes observable behavior rather than an implementation guess.

Trace each criterion to the approved problem or explicit policy, include a negative or edge case and mark open decisions. A draft without owner signoff must not be labeled ready for delivery.

## Fictional worked example

Fictional input: decision=prod-31 accepts clearer invite permissions; journey=first admin invite; design=fig-12; owner=product-lead; Engineering owner=eng-lead.

Fictional output: req-31 links prod-31; criterion=admin sees selected permission before confirming; edge case=role revoked before submit; out of scope=bulk invites; open question=guest role policy; Product approval=pending; Engineering ticket write=none.

Inadequate: “Build a new invite modal next week.” Reason: It chooses implementation and date without observable criteria, policy answers or owner approval.

## Automation handoff

Engineering Delivery Coordinator may consume product-requirements-brief/v1 with source decision ID, scope revision, owners, acceptance criteria and approval states. QA verifies the eventual build independently. Builder must validate the exact artifact identity and schema before another Crew consumes it.

## Later run

On change, compare decision, design and issue revisions; retain superseded criteria and notify owners before using a new scope in delivery.

## Boundaries

Do not create or modify delivery work, approve design, promise a release or redefine scope without the accountable owner and separate write approval. Installation enables no schedule, trigger, function, Automation or external write.
