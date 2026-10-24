# Feedback & Review Analyst

This skill gives one Crew the Feedback & Review Analyst capability. It can seed a new Crew or be added to a compatible Crew. Use the customer’s support systems as sources and reviewed action destinations.

## First useful result

1. Confirm feedback sources, period, product, market, allowed quotations, privacy rule, response policy, and owners.
2. Normalize and deduplicate records by stable feedback, review, case, and account IDs; separate unique reporters from total mentions.
3. Cluster specific themes with representative source refs and observation times; report counts and source coverage without inventing prevalence.
4. Flag urgent harm or active support cases for the correct owner; link themes to existing product issues when exact evidence supports the join.
5. Return a product decision queue and optional unsent public reply drafts with approved tone, no private details, and a separate send review.

Deliver **A feedback brief with deduplicated source IDs, theme and affected segment, volume with denominator, representative quotes or paraphrases, owner, and reviewable next action.** A theme needs representative source IDs, volume and denominator, period, and limits; one loud review is not a population-wide trend.

## Source probe and acceptance

Read one bounded feedback export with source IDs, response coverage, period and segment; join a linked support case by exact ID and inspect the current product issue before claiming a match. Record unique reporters, total mentions and excluded duplicate IDs.

Recalculate one theme numerator and its source-specific denominator, show a duplicate and an excluded record, and apply the quotation/privacy policy. A public reply stays unsent; a small or partial sample cannot become a population-wide trend.

## Follow-through

Keep stable theme and feedback IDs, compare like periods and source coverage, carry unresolved owner actions, and avoid replying twice to the same review.

## Fictional worked example

Fictional input: 40 survey replies and 15 support cases for September onboarding; product=team analytics; approved source IDs and response policy=reviews-v2.

Fictional output: theme=confusing invite permissions; 9 unique reporters among 40 surveyed respondents, plus 3 linked support cases; source refs=survey:7, survey:18, case:811; coverage=one customer segment only; owner=onboarding-product; action=review invite copy and permission guidance; public reply draft=unsent; next=compare after a reviewed change.

Inadequate: “Everyone hates onboarding; reply that we have fixed it.” Reason: It invents prevalence and a fix, omits denominator and source coverage, and treats an unsent response as authorized.

## Automation handoff

Feedback to Product Action can consume feedback-theme-brief/v1 with period, source coverage, deduplicated IDs, theme, count and denominator, issue link, owner, and reviewed action. A separate response route needs exact approval and provider receipt. Builder must bind an exact artifact path and schema and validate before another Crew consumes it. Recheck current source state.

## Boundaries

Do not publish a review reply, delete feedback, change a product issue, expose customer identity, or claim a trend without review and observed source coverage. Installation enables no schedule, trigger, function, Automation, case write, notification, or customer message. Never copy another Crew’s credentials.
