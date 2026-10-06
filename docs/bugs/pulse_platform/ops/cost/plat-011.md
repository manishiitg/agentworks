[← platform / cost-telemetry](index.md)

# PLAT-011 — model configuration hides non-tier roles

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | ops |
| Area | cost |
| Summary | `HARNESS-GETLLMCONFIG-ROLES-HIDDEN-2026-08-03` |

| Coordination | Value |
|---|---|
| Assigned agent | `Claude Code` |
| Ticket state | `ui_acceptance_pending` |
| Last synchronized | `2026-08-04` |

> Claim this ticket in this file before implementation. During active work,
> update this fragment rather than the shared index; synchronize the index
> once at handoff, review, or completion.


- **Priority:** P2
- **Owner:** LLM configuration API/UI
- **Source finding:** `HARNESS-GETLLMCONFIG-ROLES-HIDDEN-2026-08-03`
- **Source database:** `Workflow/build-in-public/db/db.sqlite`
- **Problem:** `get_llm_config` shows high/medium/low execution tiers but omits
  builder, maintenance, Pulse, and Chief-of-Staff roles.
- **Impact:** the operator cannot see the role responsible for major review or
  maintenance spend.
- **Implementation (2026-08-03):** `get_llm_config` resolves and renders
  Builder, execution high/medium/low, Maintenance, Pulse, and Chief of Staff
  with provider/model, reasoning effort, inheritance source, and override
  status. Provider profiles expand through the same provider-owned defaults as
  runtime; an explicit but missing Chief-of-Staff role is honestly shown as
  unconfigured rather than assigned an invented fallback.
- **Acceptance:** resolved configuration returns every effective role, source
  of inheritance, provider/model, reasoning level, and override status.
