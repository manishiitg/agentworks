[← goals / plans-contracts](index.md)

# PLAT-329 — Consolidate Builder plan tools and reuse skill-driven design review

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | goals |
| Area | plans-contracts |
| Summary | tracks the locally implemented consolidation of typed step/route/group/conversion/maintenance tools and replacement of the dedicated plan-review launcher with existing skill-driven background review. |

| Coordination | Value |
|---|---|
| State | Implemented and verified locally; deployment pending |
| Date | 2026-09-18 |
| Owner | plans-contracts |
| Related subsystem | learnings-knowledge (review and editing guidance) |

## Problem

Builder exposes separate add/update tools for every step type, duplicate deprecated todo_task/orchestrator aliases, separate route/group mutation tools, migration tools and a dedicated review_plan background launcher. This expands the model-facing catalog and duplicates dispatch knowledge already present in typed handlers and review skills.

## Authorized change

Expose add_step(type, step), update_step(step_id, changes), manage_step_route(action, parameters), manage_group(action, parameters), change_step_type and maintain_plan(action, parameters). Infer updates from a fresh persisted plan, including nested and orphan steps. Reuse native mutation executors and their schemas rather than rewriting plan editing. Do not expose deprecated todo_task aliases. Keep create_plan, delete_plan_steps, validate_plan_change and record_plan_drift_review distinct.

Remove review_plan from Builder's exposed surface. The existing design-plan skill checklist and run_in_background provide read-only design review. A parent reviewer executes the checklist directly rather than starting nested reviewers. Preserve the distinction from review-artifact-drift, which has bounded repair authority and durable receipts.

Keep update_validation_schema (canonical plan contract) separate from update_step_config(validation_schema) (higher-priority config override); changing the tool count must not silently change persistence targets.

Declare canonical tools in AgentWorks product.yaml; update actual registration, construction-time catalogs, schedule collision guards and attached guidance. Run mode receives no mutation tools. Existing stable native external API handlers and changelog operation names remain compatibility/internal contracts; legacy tools must not be exposed to Builder.

## Acceptance and evidence

- Actual Builder registration matches product.yaml; removed names are absent.
- Native type/action schemas reject unknown or mismatched arguments before writes.
- Existing native handlers preserve permissions, privileged write scope, graph/dependency checks, conversions and changelog records.
- Updates resolve current persisted type, including nested and orphan steps.
- Group and route handlers retain existing checks; schedule conflict protection applies to canonical names.
- Read-only design-review guidance uses existing background execution and cannot imply drift-repair authority.
- Meaningful adapter, registration, guidance and existing mutation regressions pass.

Builder catalog reduced from 114 to 89 tools. The removed review launcher is replaced by `run_in_background(access_mode="read_only")`, which removes workflow write paths, mutation tools, external MCP servers and injected secrets, while retaining the existing workspace isolation and inspection tools.

Validation passed on 2026-09-18:

- Full `step_based_workflow`, `cmd/server/guidance` and `internal/agentworksproduct` Go suites.
- Server AgentWorks/Crew product surface E2E, toolset invariants and platform ticket index integrity checks.
- Adapter regressions for current persisted types, nested/orphan updates, native config defaults and changelog identity, rejected mismatched payloads, restricted conversions and schedule collisions.
- Read-only background access validation and tool filtering; guidance rendering and skill metadata limits.
- `git diff --check`.

No server deployment or live workflow modification is part of this ticket's local implementation. The running backend must be restarted to load these changes.

## Register notes

[PLAT-329](plat-329.md) tracks the locally implemented consolidation of typed step/route/group/conversion/maintenance tools and replacement of the dedicated plan-review launcher with existing skill-driven background review. Validation and deployment status are recorded in the ticket.
