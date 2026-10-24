# Revenue Operations Analyst

This skill gives one Crew a GTM capability. It can seed a new Crew or be added to a compatible Crew. Customer source systems and owner policies remain authoritative.

## First useful result

1. Freeze the offer, campaign, period, identity rule, stage definitions and attribution model version.
2. Deduplicate events and join form, lead, account and opportunity records by allowed stable IDs; report unmatched and late records.
3. Recompute each stage count and transition with source coverage; distinguish created pipeline from influenced pipeline.
4. Return data-quality actions and an owner-reviewed measurement decision without inferring revenue lift from correlation.

Deliver **A source-linked GTM funnel reconciliation with deduplicated counts, stage definitions, join coverage, attribution limits and owner actions.** Record source IDs, revisions, observed times, coverage, owner and unknowns.

## Source probe and acceptance

Read two form events, corresponding CRM lead revisions and one opportunity with campaign or source keys. Demonstrate a duplicate and an unmatched event, plus the approved lookback window.

Recompute unique leads and accepted opportunities, list join coverage and suppress unknown attribution. A form event, CRM lead and opportunity are different objects and must not be counted as the same outcome.

## Fictional worked example

Fictional input: campaign=cmp-17; period=September; form events=e-91,e-92,e-93; CRM leads=l-55,l-56; attribution=first-touch-v2.

Fictional output: e-92 duplicates e-91; e-91 joins l-55; e-93 has no CRM match; unique submitted=2, CRM matched=1, sales accepted=0; source coverage=one of two; pipeline generated=unknown; owner action=repair e-93 join.

Inadequate: “Three submissions generated three qualified opportunities.” Reason: It counts a duplicate, skips lead acceptance and opportunity evidence, and invents attribution.

## Automation handoff

GTM Strategy Analyst and Launch Coordinator may consume gtm-funnel-reconciliation/v1 with exact campaign, model revision, stage counts, unmatched IDs, coverage and owner decision for a later launch review. Builder must validate the exact artifact identity and schema before another Crew consumes it.

## Later run

Reprocess the same event and CRM IDs idempotently, allow documented source lag and compare only like windows and stage rules.

## Boundaries

Do not rewrite attribution, change CRM stages, merge identities, spend budget or claim generated revenue without reviewed source and owner approval. Installation enables no schedule, trigger, function, Automation or external write.
