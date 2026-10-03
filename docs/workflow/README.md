# Workflow Docs

These docs cover workflow design, execution, and workflow-scoped runtime behavior.

## Includes

- Canvas and workflow UX
- Running workflows and workflow manifests
- Step configuration and execution modes
- Learning, evaluation, and monitoring
- Workflow-only interaction patterns such as human feedback and `todo_task`

## Files

- `self_improvement_and_reporting.md` — **start here.** Product overview of the unified Pulse review/fix/goal-improvement system, notifications, and Org dashboard
- `pulse_consolidation.md` — canonical current Pulse architecture and concise dated decision map; older proposal/review documents are retained as historical evidence
- `auto_improvement_framework.md` — metric-backed workflow improvement: metrics as evidence, harden/replan decisions, schedules, and audit logs
- [Browser workflow authoring](../core/browser.md#workflow-authoring) — durable selectors, browser discovery, live control and the proposed teaching flow
- `cost_and_log_measurement.md` — token usage, cost files, phase/run aggregation, and log storage
- `deterministic_routing.md` — route-by-file routing: deterministic switch, route file producers, and `run_workflow` `route_selections`
- `human_feedback_system.md` — human-in-the-loop requests, UI responses, and Slack notification fallback
- `iteration_run_folder_architecture.md` — run folder layout and iteration/run isolation
- `learn_code_flow.md` — learn-code and code-execution modes
- `learning_architecture.md` — global workflow skill, step-level learning metadata, and saved scripts
- `persistent_stores_design.md` — workflow `db/`, report views, knowledgebase updates, and structured persistent outputs
- `pre_validation_guide.md` — deterministic pre-validation schemas and consistency checks
- `step_config_format_specification.md` — `step_config.json` schema and step-level runtime configuration
- `tiered_llm_allocation.md` — tiered model selection and phase LLM allocation
- `orchestrator-step-type.md` — orchestrator (formerly todo_task) step behavior
- `tool_filtering_system.md` — workflow tool filtering and runtime tool availability
- `workflow_builder_commands_and_tools.md` — builder slash commands, privileged tools, and audit flows
- `workflow_builder_interactive.md` — workflow phase chat and interactive builder internals
- `workflow_manifest_architecture.md` — manifest format, cleaning rules, and workspace state
- `workflow_monitoring.md` — run history, costs, reports, and monitoring surfaces
- `workflow_scheduling.md` — scheduled workflow execution, routing, and history
- `workflow_shell_working_directory.md` — shell working directories and FolderGuard scope during workflow runs
