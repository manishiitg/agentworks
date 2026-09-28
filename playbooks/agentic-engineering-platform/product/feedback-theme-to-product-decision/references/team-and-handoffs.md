# Feedback theme to product decision handoff

## Journey

1. Builder inspects existing Feedback & Review Analyst and Product Feedback Coordinator Crews, owners, source grants, product issue tracker and feedback privacy scope. Propose reuse or reviewed creation.
2. Feedback Analyst binds tenant, product, segment, channels and period. It deduplicates stable record IDs and emits `feedback-theme-brief/v1` with numerator, denominator, representative source references, counterexamples, coverage and a tentative issue match.
3. Validate before Product sees the brief. Reject duplicated IDs, numerator above denominator, missing period/channel or evidence, and a claim about all customers when the source is one segment.
4. Product Feedback Coordinator re-reads current issue, roadmap and product evidence. It emits `product-feedback-decision/v1` with exact theme citation, issue match status, decision options, owner state and next verification. A related issue is not automatically an exact duplicate.
5. Validate the join and owner state. A new issue, priority change, public roadmap item or customer reply needs its own approval, current-state check and provider receipt.
6. Repeat only when a comparable feedback window or product state changes. Retain stable theme and issue IDs and do not reopen a declined decision without new evidence.

## Contract

Both artifacts share tenant, product, theme, period and segment. The Product artifact cites the exact validated Feedback artifact and repeats its numerator, denominator and coverage note. It must record current issue source observation, including an explicit no-match result when nothing is found. The decision may be `investigate`, `link_existing`, `defer`, `decline` or `pending_review`; `link_existing` requires an exact matching issue ID and evidence. `issue_action_state=none` has no approval or provider receipt; `created` or `updated` needs exact owner approval and a matching provider receipt. The validator checks mechanical claims; the product owner decides priority and treatment using the real source records.

## Manual proof

Run one authorized feedback theme with real source coverage and current issue state. Save validator results, product owner corrections and the absence or presence of issue or customer-action receipts. Fictional examples do not complete setup.
