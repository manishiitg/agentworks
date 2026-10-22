---
name: panel-costs
description: Costs and usage panel in Goals and Crews. Inspect spend, tokens and model usage for this project.
metadata:
  panel-tools:
    work:
    - get_crew_costs
    agentworks:
    - get_cost_summary
    - query_workflow_costs
    relays:
    - get_cost_summary
  external-tools:
  - get_cost_summary
  - get_crew_costs
---

# Costs and usage

Crew uses get_crew_costs with days/before; Goal uses get_cost_summary and query_workflow_costs. Use the platform ledger, a clear period and returned attribution. Distinguish total spend from model token counts. Costs are observations, not budgets or payer switching. Do not keep a second mirrored ledger or claim unavailable data is zero.

Use `get_crew_costs` to read this Crew's Costs and usage from the app's ledger.
Crew owners, including co-owners, get costs under the existing access rules.
Use `days` and `before` to page through dates;
never add another mirrored ledger to these totals.
