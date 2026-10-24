# Release Quality Assistant

This skill gives one Crew the Release Quality Assistant capability. It can seed a new Crew or be added to a compatible Crew without replacing its identity. Use the customer’s approved test and release systems.

## First useful result

1. Bind the requested release, commit SHA, build artifact, deployment environment, policy revision, and cutoff time.
2. Enumerate every required suite and group from policy before reading results; join results only by exact tested identity.
3. Treat missing, cancelled, stale, skipped, blocked, or mismatched results as nonpassing. Preserve distinct rerun attempts.
4. Derive pass, fail, or needs-review from the configured rule and show evidence and owner decisions for every exception.
5. Publish a status only through an authorized route and retain the provider receipt; a proposed or attempted status is not a published gate.

Deliver **A release quality brief with exact commit/build and environment, required suite matrix, missing or stale evidence, and proposed pass, fail, or needs-review decision.** A green CI summary alone does not prove the required suites ran against the reviewed build. Show policy, tested identity, source IDs, and receipt.

## Source probe and acceptance

Read the release policy revision and enumerate its required suites before fetching CI results. Join each run to the exact commit, build artifact, environment, suite revision, and cutoff; include a missing or mismatched result in the review.

Recompute the gate verdict from the complete required-suite matrix. Prove that a green CI summary with a missing suite, stale result, different SHA, or unresolved waiver remains nonpassing; record the release owner decision separately from any published status.

## Follow-through

Re-evaluate each new build independently; retain prior failures and waivers, and never reuse a passing result from a different SHA or environment.

## Fictional worked example

Fictional input: release=rc-28; SHA=987fed; build=artifact:rc-28-987fed; environment=preview-28; policy=gate-v5 requiring auth and critical-journey suites; owner=release-lead.

Fictional output: release=rc-28; SHA=987fed; build=artifact:rc-28-987fed; environment=preview-28; policy=gate-v5; auth=pass run:auth-19 on same build; critical-journey=missing; verdict=needs_review; owner=release-lead; published_status=none; next=run required journey suite on exact artifact.

Inadequate: “CI is green; release approved.” Reason: It does not bind the reviewed artifact or account for the missing required journey suite and lacks an owner decision or delivery receipt.

## Workflow Playbook and handoff

The Release and PR Quality Gate Workflow can consume release-quality-brief/v1 with release, SHA, build, environment, policy revision, required-suite matrix, verdict, reviewer, and delivery receipt. Engineering may consume a separate blocker ledger for fixes. The existing Workflow Playbook is browser-qa/release-pr-quality-gate. Builder must bind the exact output path and schema before another Crew consumes a result. Confirm source truth and customer policy independently.

## Boundaries

Do not merge, deploy, waive a required suite, auto-approve needs-review, or post an external status without the release owner and authorized action route. Installing this skill enables no schedule, trigger, function, Automation, notification, or write. Do not copy another Crew’s credentials.
