# Browser Journey QA Analyst

This skill gives one Crew the Browser Journey QA Analyst capability. It can seed a new Crew or be added to a compatible Crew without replacing its identity. Use the customer’s approved test and release systems.

## First useful result

1. Confirm the journey revision, expected outcome, exact build, environment, account, test-data bounds, and recording policy before execution.
2. Reuse a verified browser profile and canonical locators. Record each attempt with start time, result, screenshots or video, console/network evidence status, and source revision.
3. Distinguish product failure, test failure, environment blocker, and unresolved evidence. A retry is a separate attempt and never erases the first failure.
4. Return reproduction steps, likely owner, missing evidence, and the review needed before changing a test or product.
5. Retest the same approved journey against a named later build and report both observations.

Deliver **An attempt-level journey result with exact build and environment, pass/fail or blocked status, reproduction steps, and linked visual and console/network evidence.** A pass requires all required assertions and evidence; skipped, blocked, missing, or stale attempts remain visible and cannot become a pass.

## Source probe and acceptance

Run one approved journey against a pinned build and environment using an authorized test account. Save the attempt ID, required assertion results, recording and console/network artifact IDs, and one failed or blocked step. A successful retry must remain a separate attempt.

Reproduce one observed failure from the saved attempt on the same build, or mark it unresolved with the missing evidence. Check that a skipped assertion, absent required recording, or blocked account cannot yield pass.

## Follow-through

On a later run, retain earlier attempts, compare the same journey and build identity rule, and record changed test or environment revisions before claiming a regression was fixed.

## Fictional worked example

Fictional input: journey=signup-to-project@v3; build=sha:abc123; environment=preview-17; expected=new user creates a project; account=test-user-17; required evidence=video and console/network log.

Fictional output: attempt=qa-17-01; journey=signup-to-project@v3; build=sha:abc123; environment=preview-17; observed=project creation returned HTTP 403 after signup; verdict=fail; video=asset:qa-17-01.mp4; console/network=asset:qa-17-01.json; reproduction=sign in as test-user-17, submit project form, observe 403; owner=project-api-owner; retest=pending.

Inadequate: “Signup failed; retry later.” Reason: It omits the tested build, expected step, attempt, evidence, reproducible failure, and owner.

## Workflow Playbook and handoff

The Critical Journey Validation Workflow can consume a journey-result/v1 record keyed by journey revision, build, environment, attempt ID, observed outcome, evidence links, classification, and owner. A release gate must verify exact build identity and required suite completeness. The existing Workflow Playbook is browser-qa/critical-journey-validation. Builder must bind the exact output path and schema before another Crew consumes a result. Confirm source truth and customer policy independently.

## Boundaries

Do not change production data, weaken assertions, approve a release, or create a ticket or message without the authorized action route and owner review. Installing this skill enables no schedule, trigger, function, Automation, notification, or write. Do not copy another Crew’s credentials.
