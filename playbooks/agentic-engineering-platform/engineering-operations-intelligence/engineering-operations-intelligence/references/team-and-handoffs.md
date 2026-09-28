# Engineering metric to owned improvement

## Route

1. Builder binds a single team/service, versioned metric rule, exact source and identity mapping, equal windows, predeclared target, coverage minimum and owner. The broader Engineering Operations Intelligence data foundation and review guidance remains available for later metrics.
2. Engineering Operations Analyst deduplicates authorized items or events and emits `engineering-metric-observation/v1`: numerator, denominator, rate, source references, coverage, baseline/comparable/not-evaluable state and limits. A partial source is not a target miss; one window is not a trend. A changed policy or team scope resets comparison.
3. A blocking validator checks the observation before Engineering Delivery Coordinator reads it. Delivery re-reads current issue and ownership state, binds the exact observation ID and prepares `engineering-improvement-review/v1` with one bounded action question, owner, existing issue match, stable case key and review state. No ticket is silently created.
4. Validate the pair. Ask the owner to accept, defer or reject the proposed investigation. A separate action route re-reads the exact issue, obtains approval, records a provider receipt and later observes the same metric under a compatible rule. The later metric can show change but does not prove the action caused it.

## Identity and repeat

Match tenant, team, service, environment, metric ID/policy revision, population and current window across artifacts. Keep case key and prior case artifact on updates; retain corrected source snapshots. Later windows must be equal duration and nonoverlapping under the same policy, identity and coverage. Use explicit new baselines for changed definitions.

## Example acceptance

The fictional platform team had 6/75 blocked open items (8%) in a prior week and 12/80 (15%) in the current week, above a predeclared 10% maximum. Delivery reads current issue ownership and prepares an unsent owner question. A baseline-only or partially covered window cannot claim trend. The rejected review claims a personnel cause, issue creation and verified improvement without evidence; the validator stops it. Fixtures do not prove a customer's data or an outcome.
