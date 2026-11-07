# Consolidated plan editing

Builder uses the tools declared by AgentWorks product.yaml. Run has no plan mutation authority. Reading this reference never grants tools or permissions.

- `create_plan()` initializes the empty plan; it never overwrites an existing one.
- `update_step(step_id, changes)` infers the type from the current persisted plan, including nested and orphan steps. `changes` contains native update fields and `reason`, without `existing_step_id`. Do not send fields for another step type. Example: `update_step(step_id="fetch", changes={"title":"Fetch invoices", "reason":"Clarify scope"})`.
- `delete_plan_steps(deleted_step_ids, reason)` retains dependency-safe deletion.
- `convert_routing_branch_step_type(existing_step_id, target_type, reason)` converts only `routing` ↔ `branch`, preserving identity and the plan changelog.
- `manage_step_route(action, parameters)` supports `add`, `update`, `delete` for an agent's `predefined_routes` and persisted legacy agent records. `parameters` uses the selected native schema, including `parent_step_id`, `reason` and the corresponding new-route/update/delete fields.
- `maintain_plan(action, parameters)` supports `cleanup_orphan_configs`, `migrate_agent_code`, `migrate_orchestrator_types`, `migrate_execution_modes`, `strip_execution_modes`. These are explicit maintenance operations, not substitutes for normal editing. Only advertised actions are available in a particular caller.

`update_validation_schema(existing_step_id, validation_schema, reason)` changes the canonical plan contract. `update_step_config(step_id, validation_schema, reason)` sets a config override, which takes precedence over that plan contract. These remain separate tools because they write different stores. Neither is a substitute for the other. Do not silently create an override when the user requested a plan change.

The adapter validates the selected native schema before invoking the existing handler. Unknown fields and mismatched type/action payloads are rejected before writes. Those handlers retain privileged write access, permissions, graph validation, schedule collision guards and native changelog receipts. Internal receipts may name the native operation; that does not expose a legacy tool to the model.

Use `validate_plan_change` for deterministic checks after editing. Use the `design-plan` command checklist for read-only design review. When a separate reviewer is appropriate, give it to one of your own subagents with explicit read-only instructions and confirm nothing changed; do not nest further reviewers. `run-plan-drift` is different: it follows its own review-and-repair contract. Persist step drift checks through `record_plan_drift_review`; do not turn a read-only design review into automatic repairs.
