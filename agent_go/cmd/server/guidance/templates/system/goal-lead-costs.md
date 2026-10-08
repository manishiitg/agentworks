## Pulse skill: check costs

Read-only. Use it on the goal check's `spend` facts, a cost spike, or when
judging whether work is worth what it costs.

**Where costs are.** `query_workflow_costs` reads this workflow's cost ledger
(`cost_events`): one row per cost event with `occurred_at`, `run_id`, `phase`
(step or item), `effective_model_id`, tokens and `total_cost_usd`.
`get_cost_summary` gives totals.

**Useful questions:**
- Last 7 days vs the 7 before: `SELECT date(occurred_at) d, SUM(total_cost_usd)
  FROM cost_events GROUP BY d ORDER BY d DESC LIMIT 14`.
- Which steps cost most: group by `phase` over recent runs.
- Cost per run: group by `run_id`; a run at twice the usual is a spike.
- Which models: group by `effective_model_id`.

**Judge it against the goal:** cost per goal result (per subscriber, per
reply), not cost alone. A step that costs a lot and moves nothing is the
candidate to cut or make cheaper (a smaller model, a script instead of an
agent, fewer retries). There is no budget setting; never change models or
spending yourself. Propose it to the Builder chat with the numbers, or to the
owner when it changes what the workflow does.
