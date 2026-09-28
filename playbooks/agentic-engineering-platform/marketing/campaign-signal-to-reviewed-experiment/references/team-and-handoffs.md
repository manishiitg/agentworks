# Campaign evidence and experiment handoff

## Customer journey

1. **Discover:** Builder inspects existing Crew IDs, campaign account and offer, platform and CRM sources, current and comparable baseline periods, attribution window and data lag. It proposes reuse or reviewed creation before applying any steps.
2. **Measure:** Campaign Performance Analyst calculates the same qualified-events-per-click metric in both periods from deduplicated downstream events and platform clicks. It cites spend, event and denominator records, coverage, currency and confounders. The brief labels a change as observed, not caused.
3. **Validate:** Run the package validator on campaign-performance-brief/v1 before another Crew consumes it. A baseline with a different campaign, attribution window or event definition is a different comparison.
4. **Context, optional:** Competitor Intelligence Analyst can attach competitor-context/v1 for the same own offer and market with dated prior/current primary sources. It cannot supply conversion counts or prove why the campaign changed.
5. **Plan:** Growth Experiment Planner writes growth-experiment-plan/v1 for the validated campaign brief. The plan carries the same metric, a falsifiable hypothesis, eligible unit, one treatment, primary and guardrail metrics, baseline, sample or duration rule, stop condition, owner and explicit proposal state.
6. **Act separately:** Owner review of a plan is not publication. Builder must create a separate exact action route with approval, change target, idempotency key, rollback and provider receipt before a variant, budget or message can change.
7. **Verify:** Later re-read the experiment platform and comparable qualified events. Report the predeclared result and guardrails, including null or incomplete results, without moving the decision threshold after seeing data.

## Identity and handoff rules

Performance and plan match tenant, account, campaign, offer, market, metric and attribution window. The plan cites the exact performance brief. Current and baseline numerator/denominator pairs are nonnegative, separately sourced and arithmetically consistent. The optional competitor artifact matches own offer and market but remains a context source. Source coverage and data lag appear in the owner review. A plan in this package is a proposal and has no activated variant.

## Example acceptance

The fictional examples show a drop in qualified demos per click from 4% to 2% with the campaign source and CRM coverage recorded. The experiment plan tests one message hypothesis with a primary and guardrail metric but does not publish it. The invalid example claims the experiment is active from a plan alone and fails. Fixtures teach the contract and do not complete customer setup.
