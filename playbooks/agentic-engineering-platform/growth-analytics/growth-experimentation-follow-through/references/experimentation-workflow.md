# Experimentation workflow

## Freeze the source and decision

Start with a source-linked hypothesis and a versioned plan. Record product and tenant, eligible population, assignment unit, control and treatment IDs, allocation, primary and guardrail metric definitions, minimum sample, observation window, stop rule, rollback owner and decision owner before launch. Campaign, funnel, retention, SEO and AI-visibility signals can motivate a plan; none proves that a proposed treatment will work. A source proposal is not an approval record.

## Verify execution

Use Experiment Run Coordinator to check the exact plan revision and dated owner decision against the current provider object. An issue tracker task may coordinate work but cannot prove a flag, page, campaign or message is live. A launched record needs the provider receipt, configuration revision, launch time and exposure source; a pending decision remains pending. A separate authorized action route owns writes and rollback. Use stable IDs to prevent duplicate action and record configuration drift.

## Measure and review

Use Growth Outcome Analyst after a provider-confirmed launch. Join exposure to authorized primary and guardrail outcome sources under the frozen identity and event rules. Distinguish eligible, exposed, excluded and unmatched units. Wait for the declared outcome window and source lag. Recompute numerators, denominators, rates, guardrail threshold and sample gate. Record instrumentation changes and contamination. An early, underpowered or breached result is inconclusive even if treatment's observed rate is higher. A numerically complete result still needs analysis and owner review before any ship/iterate/stop decision.

## Handoff and repeat

The typed path is `frozen-experiment-plan/v1` plus `experiment-execution-record/v1` → `experiment-outcome-readout/v1`, with blocking validators at both steps. Return exact plan, decision, provider and source IDs, artifact paths, maturity, rates or unknowns, limitations and owner decision. The [team guide](team-and-handoffs.md) contains examples and commands. Preserve prior readouts and source revisions; corrections supersede rather than silently overwrite. Keep any schedule paused until manual evidence, window timing, source freshness, cost and notifications are reviewed separately.
