# Sales Enablement Coordinator

This skill gives one Crew a GTM capability. It can seed a new Crew or be added to a compatible Crew. Customer source systems and owner policies remain authoritative.

## First useful result

1. Bind offer version, persona, sales stage and content approver.
2. Read current product, pricing, legal and customer proof sources; classify each claim as approved, conditional or unsupported.
3. Join recurring buyer objections to exact call or CRM records and draft evidence-bounded responses.
4. Prepare the seller asset with version, owner, expiry and missing-proof list; keep distribution pending approval.

Deliver **A seller-ready enablement brief with persona, approved claims and source versions, objection guidance, proof limits, owner approvals and expiry check.** Record source IDs, revisions, observed times, coverage, owner and unknowns.

## Source probe and acceptance

Read one current offer and price revision, one approved proof artifact and two dated objection records for the same buyer segment. Test one stale deck claim against the current source.

Every proposed claim has a current source and permitted use; stale pricing and unconsented customer names are removed. A useful draft remains unpublished until the content owner approves it.

## Fictional worked example

Fictional input: offer=team-analytics-v2; persona=RevOps; deck=deck-7; price=price-v4; calls=call-21,call-24; owner=enablement-lead.

Fictional output: brief=enable-7; “exports in one click” approved by docs-v5; “saves 10 hours” unsupported; price on deck-7 stale versus price-v4; objection=setup effort from two calls; next=review revised deck; distribution=pending.

Inadequate: “Send every prospect the old deck and promise a 10-hour saving.” Reason: The claim has no approved proof, pricing is stale and the template has no contact authority.

## Automation handoff

Sales Follow-up Coordinator may consume gtm-enablement-brief/v1 with exact offer, claim versions, persona, limitations and approval state; it must recheck contact permission and current account context. Builder must validate the exact artifact identity and schema before another Crew consumes it.

## Later run

Revalidate claim and price revisions before reuse; retire stale assets and preserve the owner-approved version rather than silently overwriting it.

## Boundaries

Do not publish assets, email prospects, disclose customer identities, change pricing or make contractual promises without content and account-owner approval. Installation enables no schedule, trigger, function, Automation or external write.
