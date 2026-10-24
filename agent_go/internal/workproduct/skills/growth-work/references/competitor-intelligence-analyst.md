# Competitor Intelligence Analyst

This skill gives one Crew the Competitor Intelligence Analyst capability. It can seed a new Crew or be added to a compatible Crew. Use the customer’s systems as sources of record and reviewed action destinations.

## First useful result

1. Bind own offer, target buyer, geography, competitor product and plan IDs, topics, baseline date, source scope, and decision owner.
2. Capture dated primary pages, changelogs or owner-supplied sales records; preserve the exact URL or record revision and avoid hidden or restricted material.
3. Compare like-for-like plan, region, seat, usage and terms. Label source claims, observed changes, estimates, and unavailable evidence separately.
4. Score relevance against the owner’s stated positioning or sales question; do not infer customer preference from a vendor page.
5. Deliver a short change brief with implications to review, not an automatic pricing or messaging change.

Deliver **A dated change brief with exact competitor/product/plan, primary source and capture time, prior-versus-current evidence, relevance, uncertainty, and owner question.** Each change needs before/after source IDs and capture times, or an explicit baseline gap; a copied claim is not independent verification.

## Source probe and acceptance

Capture the current primary vendor page and an earlier dated snapshot for the same product, plan, region, seat and term; record URL, revision, capture time and any inaccessible terms. An owner-supplied sales note may explain relevance but cannot replace a missing before snapshot.

Verify one claimed change against two comparable captures and show one unchanged or unverifiable claim. If region, plan, pricing basis or baseline differs, label the comparison unknown rather than a change or market trend.

## Follow-through

Retain watched URL/product keys and prior captures, compare only changed facts, recheck expiring prices and terms, and suppress unchanged alerts.

## Fictional worked example

Fictional input: offer=team analytics; buyer=20-seat SaaS operations team; competitors=A Pro and B Team; region=US; baseline=2026-09-01; watch=API export and annual price.

Fictional output: A Pro now lists API export at 25 seats, versus 10 seats on archived 2026-09-01 page; current source=A pricing@2026-09-26; B Team price claim unchanged but contract term unavailable; relevance=medium for 20-seat buyers; owner question=check whether sales objections mention export thresholds.

Inadequate: “Competitor A is winning because it has better pricing. Cut our price today.” Reason: No matched plan, dated source, comparable terms, customer evidence, or owner approval supports the conclusion or action.

## Automation handoff

Campaign Signal to Reviewed Experiment may attach competitor-context/v1 to an experiment plan only if competitor product, market, source window and own offer are bound. Competitive context cannot substitute for campaign performance evidence. Builder must bind an exact artifact path and schema and validate before another Crew consumes it. Re-read current source state.

## Boundaries

Do not scrape behind access controls, copy protected customer data, claim market share, change prices, publish claims, or contact a competitor from installation. Installation enables no schedule, trigger, function, Automation, external write, notification, or customer message. Never copy another Crew’s credentials.
