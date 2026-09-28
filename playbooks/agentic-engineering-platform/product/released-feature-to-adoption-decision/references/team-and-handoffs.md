# Released feature adoption to product decision

## Route

1. Builder verifies an observed release/build and flag revision, product/tenant scope, eligible segment, distinct-account join, predeclared use predicate, target and owner. It inspects existing Crews and source access before proposing reuse or reviewed creation.
2. Product Adoption Analyst records one window of eligible, exposed and used distinct accounts with source references and coverage. `used` is a subset of `exposed`, which is a subset of `eligible`. A complete window with enough exposed accounts is a baseline; a second equal-duration, nonoverlapping window under the same release/flag/rule permits an observed trend. Partial coverage or too few exposed accounts is `not_evaluable` even when some counts exist.
3. A blocking validator runs before Product receives the observation. Product Feedback Coordinator re-reads the current release and issue source and prepares a decision artifact with the exact upstream ID. It records related versus exact issue matches and the evidence gaps. The owner may investigate, iterate, keep or stop, but a decision is still separate from an issue write or feature-flag action.
4. Validate the pair, then show the owner the actual counts, source coverage, target rule, contrary evidence and current issue state. Save a manual run before recurrence. A later action route must re-read the release and issue target, get exact approval and retain provider receipts.

## Identity and repeat

Match tenant, product, feature, release, build, flag revision, segment, measurement and identity rules, and window exactly across artifacts. Keep a case key and prior artifact ID on repeats. Corrected late events create a dated revision, not a silent rewrite. Changing instrumentation, flag, eligible population or use predicate starts a new baseline; it cannot be compared as the same trend.

## Example acceptance

The fictional baseline has 120 eligible accounts, 80 exposed and 24 used: 66.67% exposure, 30% use among exposed, below the predeclared 40% target with enough sample. A later complete equal window can be compared. The incomplete case keeps the target unknown. The rejected decision claims a causal retention loss, owner approval and a product change from one window; the validator stops it. Fixtures do not prove a real release, customer source or business impact.
