# Product Feedback Coordinator

This skill prepares a product owner decision from current, authorized feedback and product records. It does not authorize a roadmap or issue-tracker change.

## First useful result

1. Bind exact tenant, product, theme, period, affected segment, owner and feedback source scope.
2. Inspect the validated theme and its deduplicated source IDs, denominator, coverage and counterexamples. Separate reported pain from an observed product defect.
3. Re-read current issue, roadmap, release and product-usage records. Match an existing issue by exact problem and scope; flag weak or ambiguous matches.
4. Compare owner criteria: severity, reach, segment, strategic relevance, evidence quality, effort unknowns and existing commitments. A count alone is not a priority score.
5. Present options: investigate, link to an existing item, prepare a new candidate, decline, or defer. Record the product owner decision and evidence needed next.

Deliver **A product decision brief with exact source artifact, affected scope, current issue state, evidence gaps, owner options and next verification.** Every factual claim needs feedback or product source IDs, observed time, coverage and an owner decision state.

## Route-specific source probe and acceptance

Feedback route: read one validated theme by artifact ID, its deduplicated case or survey source IDs, eligible response denominator and contrary examples, then the current issue and roadmap records for the same product and segment. Record revisions and observed times; a related issue title is not an exact issue match.

Recompute the theme prevalence from the stated population, keep linked support cases separate from survey respondents, and show why a current issue is exact, related or unmatched. Setup remains pending if a missing denominator becomes “everyone,” a related issue becomes a fix, or a customer promise is drafted without owner authority.

Released-feature route: read the validated observation artifact, exact deployed release/build and flag revision, eligible and exposed account sets, versioned use event rule, observation window and current issue state. Check account identity, exclusions, coverage and source lag under the same segment.

Recompute used/exposed and exposed/eligible rates, compare only windows with the same rule and mature coverage, and label one complete window a baseline. Setup remains pending if missing exposure or use events are treated as zero, an immature window yields a winner, or a metric change is called causal.

## Fictional worked example

Fictional input: theme F-9 reports confusing invite permissions from 9 of 40 surveyed respondents plus 3 linked cases, all from one segment. Current tracker issue ISSUE-18 concerns a different admin permission error; roadmap has no committed invite change.

Fictional output: decision brief P-9 binds F-9 and its 40-response denominator; affected segment=one onboarding cohort; ISSUE-18 is related but not an exact duplicate; observed usage impact=unknown; recommendation=investigate invite copy with Product and Support; owner decision=pending; new issue=none; customer promise=none.

Inadequate: “Everyone has this bug. ISSUE-18 fixes it; promise customers the change next week.” Reason: prevalence, issue identity and delivery date are unsupported, and no product owner approved a commitment.

## Released feature decision

For a validated `feature-adoption-observation/v1`, bind exact tenant, product, release/build, flag revision, feature, segment and observation window. Re-read current issue and release state. Preserve eligible, exposed and used denominators, coverage, sample minimum and predeclared target. Present investigate, iterate, keep or stop as owner options; one window cannot prove a trend or cause. Emit `feature-adoption-decision/v1` linked to the exact observation artifact for a blocking handoff check.

Fictional input: release R-7 / flag f7-r2 exposed 80 of 120 eligible accounts; 24 exposed accounts used the feature during a complete week under rule U-1. Target is 40% use among exposed accounts. Current issue ISSUE-31 concerns a related onboarding step but is not an exact duplicate.

Fictional output: decision P-31 cites the 24/80=30% observation, complete coverage, one baseline window and ISSUE-31 current state. Recommendation=investigate the exposure and use path with the owner; decision=pending; issue write=none; customer promise=none. Inadequate: “This feature caused retention to fall; stop it and close ISSUE-31.” One week of use and an unrelated issue cannot prove cause, and no owner approved those actions.

## Later run

Re-read current issue, roadmap, release and product evidence; retain theme or feature observation IDs, rule versions and owner decisions, and report changes instead of opening duplicate work.

## Boundaries

Do not promise a feature, rank a roadmap by mention count alone, create or close an issue, change priority, publish a plan, or message a customer without owner approval and a separate route. Installation enables no schedule, trigger, function, Automation, issue write or customer message.
