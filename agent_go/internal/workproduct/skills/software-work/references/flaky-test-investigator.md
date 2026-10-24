# Flaky Test Investigator

This skill gives one Crew the Flaky Test Investigator capability. It can seed a new Crew or be added to a compatible Crew without replacing its identity. Use the customer’s approved test and release systems.

## First useful result

1. Bind one canonical test, source revision, build, browser profile, environment, fixtures, and approved expectation.
2. Compare original and repeated isolated attempts with consistent inputs; retain both passing and failing traces and timestamps.
3. Check order dependence, shared state, network conditions, product races, locator drift, and environment outages; label the cause unresolved when evidence is insufficient.
4. Propose a bounded experiment and exact candidate fix with owner, risk, and verification criterion. Preserve original assertions.
5. After any approved change, rerun the canonical test under the agreed stability policy and keep its earlier failures visible.

Deliver **An attempt-by-attempt flake investigation with controlled inputs, failure and pass evidence, cause hypotheses, and a reviewable stabilization proposal.** A green retry is not proof of stability; report attempt count, result distribution, environment and build identity, and confidence limits.

## Source probe and acceptance

Fetch the canonical test revision and at least one original failed attempt plus its retry for the same build, environment, browser profile, and fixtures. Save each CI run and trace ID; a green summary without the original failure is insufficient.

Run or inspect a bounded set of isolated attempts under fixed inputs, retain the result distribution, and classify product, test, environment, or unresolved. Reject a proposed wait, quarantine, or assertion change when the cause is still unresolved.

## Follow-through

Use the same test and build keys to compare new attempts; reopen a prior classification when product or environment evidence contradicts it.

## Fictional worked example

Fictional input: test=login-refresh@v4; build=sha:def456; environment=staging-eu; fixtures=account-22; allowed attempts=5 isolated; prior result=pass after CI retry.

Fictional output: attempts=flake-22-01..05 on sha:def456/staging-eu with same fixtures; results=fail, pass, fail, pass, fail; failing traces show refresh token response delayed after assertion; classification=unresolved product-or-environment race; confidence=low; owner=auth-team; proposal=inspect token service timing before any test change; original assertions retained.

Inadequate: “It passed on retry, so increase the wait and mark green.” Reason: A retry does not establish a test defect; this hides product and environment evidence and changes the assertion policy without review.

## Workflow Playbook and handoff

The Flaky-Test Detection and Stabilization Workflow can consume flake-investigation/v1 with stable test and build IDs, attempt IDs, evidence, classification, confidence, candidate change, approval state, and later stability check. The existing Workflow Playbook is browser-qa/flaky-test-detection-stabilization. Builder must bind the exact output path and schema before another Crew consumes a result. Confirm source truth and customer policy independently.

## Boundaries

Do not silently quarantine or skip a test, weaken an assertion, inflate waits, or change canonical test source without owner review and an authorized route. Installing this skill enables no schedule, trigger, function, Automation, notification, or write. Do not copy another Crew’s credentials.
