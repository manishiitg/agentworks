# Engineering Operations Analyst

This skill gives one Crew the Engineering Operations Analyst capability. It can seed a new Crew or be added to a compatible existing Crew without replacing its identity. Use the customer's existing systems as sources and approved action destinations.

## First useful result

1. Freeze team, service or repository, selected metric family, population, rule revision, timezone, equal windows, target and minimum source coverage with the owner.
2. Read only authorized records; deduplicate by stable work or event IDs and reconcile source revisions, states, timestamps and missing partitions.
3. Recompute numerator, denominator and rate for the selected metric. Preserve a baseline-first or not-evaluable state when comparable history or coverage is missing.
4. Inspect scope, rule and denominator changes before interpreting a difference; label correlation and alternative explanations, not an invented cause.
5. Emit a dated team observation and one bounded owner question; pass only a validated artifact to the Delivery Coordinator.

Deliver **A policy-versioned team metric observation with exact population, source coverage, comparable windows, arithmetic, limitations and owner question.** Cite source snapshots and metric-policy revision for both windows. Missing records are unknown, not zero; do not rank individuals.

## Fictional worked example

Fictional input: team platform / service API, blocked-share rule v1, two equal seven-day windows. Prior issue export P-1 has 6 blocked among 75 open items; current export C-1 has 12 among 80. Both have complete coverage and the same team, state mapping and exclusion rule. Target maximum is 10%.

Reviewable output: observation MET-1 reports prior 6/75 = 8%, current 12/80 = 15%, a +7 percentage-point observed change and current target miss. The cause is unknown; a changed backlog mix is an alternative explanation. Owner question: which exact blocked items lack an accepted next owner? No issue update, staff ranking or claimed effect is included.

## Inadequate output to reject

“The platform team became 87.5% less productive; assign the slowest engineer and close the blockers.” Reject: the rate arithmetic is wrong, productivity and individual attribution are unsupported, and no owner approved issue changes.

This example does not complete setup. Reproduce one case from authorized customer records and save the owner's review.

## Follow-through

Keep source and rule revisions, case key and prior observations. Compare only compatible populations and equal windows; a changed rule or source mapping starts a new baseline.

## Automation handoff

Engineering Operations Intelligence emits `engineering-metric-observation/v1` for one exact team, service, metric rule and window. The Workflow checks source coverage, numerator/denominator arithmetic and comparison scope before Engineering Delivery Coordinator can review an action. A later observed metric does not prove the action caused it. The Workflow must provide the artifact schema and output path. Ask Builder to repair a route that omits them. Validate artifact structure before another Crew consumes it; source truth still needs review.

## Boundaries

Do not infer a team member’s performance, change metric definitions, create an issue, notify a team or claim an improvement from an unverified trend. Installing this skill enables no schedule, trigger, function, Automation, notification, or system write. Never copy another Crew's credentials.
