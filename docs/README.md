# Documentation

Start with the operator journey, then use the subsystem references when you need implementation detail.

## Start Here

- [Getting Started](getting-started/README.md): install AgentWorks, complete first-launch setup, and create a first automation.
- [Build Your First Workflow](getting-started/first-workflow.md): define an outcome, choose a worker, run it, review evidence, and improve the next run.

## Product Areas

- [Workflow](workflow/README.md): workflow authoring, execution, scheduling, monitoring, Pulse, and Auto Improve.
- [Organization and Agents](multiagent/README.md): delegation, Org Pulse, shared memory, and agent-to-agent coordination.
- [Core](core/README.md): providers, MCP, browser sessions, connectors, secrets, security, and shared runtime services.

`docs/bugs/` is an incident archive — see [its index](bugs/README.md), which groups the 2026-08-01/02 investigations into how the agent-facing tool and permission contract actually behaves. `docs/refactor/` records implementation migrations — see [its index](refactor/README.md), where status distinguishes a shipped design from one still being built. Neither folder is the recommended entry point for operators, but the bugs index is the fastest way to understand why an agent is told one thing and the runtime does another.

## Bot Connectors & Messaging

- [Channel connectors overview](channel-connectors.md): Slack and WhatsApp at a glance.
- [Slack connections](core/slack_connections.md): per-workflow Slack apps, ownership, and the multi-listener runtime.
- [Bot connectors architecture](core/bot_connectors_combined.md): shared lifecycle, routing, and code-review findings.
- [Bot connector system](core/bot_connector_system.md): sessions, channels, and event flow.
- [QA: Slack bot connectors](https://github.com/manishiitg/coding-agent-loop/issues/201): per-workflow apps, mention guard sign-off.
- [QA: WhatsApp bot connector](https://github.com/manishiitg/coding-agent-loop/issues/202): route switching, account management sign-off.

Manual checklists live in issues, never in `docs/` — browse all QA rounds [here](https://github.com/manishiitg/coding-agent-loop/issues?q=is%3Aissue+QA).

This folder also mirrors to the [GitHub wiki](https://github.com/manishiitg/coding-agent-loop/wiki) on every push to `main` — edit here, never there.

## Placement Rules

- Put a doc in `workflow/` when it is primarily about workflow authoring, workflow execution, step configuration, or workflow-only UX.
- Put a doc in `multiagent/` when it is primarily about manager/worker delegation, multi-agent chat, or agent-to-agent coordination.
- Put a doc in `core/` when it applies across chat, workflow, and multi-agent modes or describes a foundational subsystem or integration.

## All Pages

Every page in this folder, so the index (and the wiki
mirror) never silently omits one. Curated entry points are above;
this is the complete map.

### Top level

- [Channel connectors](channel-connectors.md)
- [Google CLI authentication and agent terminals](google-cli-authentication.md)
- [Header Consolidation](header-consolidation.md)
- [Live browser in workflows](live-workflow-browser.md)
- [Outcome goals and measurable progress](pulse-goal-measurement.md)
- [Pulse review visibility](pulse-review-visibility.md)
- [Workflow improvement through Pulse](pulse-workflow-improvement-system.md)
- [Reusable report data and widgets](report-metric-widgets.md)
- [Live report data from scripts (`window.report.run`)](report-live-data.md)
- [Secrets](secrets.md)
- [Setup Consolidation](setup-consolidation.md)
- [Workspace UI Design Guidelines](ui-design-guidelines.md)

### Audits

- [PLAT-324 architecture review — 2026-09-17](audits/chat-reliability-architecture-review-2026-09-17.md)
- [Chat reliability backend implementation — 2026-09-17](audits/chat-reliability-backend-fixes-2026-09-17.md)
- [Chat reliability implementation — 2026-09-17](audits/chat-reliability-implementation-2026-09-17.md)
- [ChatTab / ChatInput / ChatArea isolation review](audits/chat-tab-input-area-isolation-review-2026-09-17.md)
- [Message-sequence runtime cleanup collision — 2026-09-07](audits/message-sequence-session-cleanup-2026-09-07.md)
- [Platform backlog reconciliation — 2026-09-05](audits/platform-backlog-reconciliation-2026-09-05.md)
- [Fallback persistence and orphan-decision cleanup](audits/platform-fallback-and-decision-fixes-2026-09-05.md)
- [Full open-report reconciliation — 2026-09-05](audits/platform-open-report-reconciliation-2026-09-05.md)
- [Schedule runtime projection fix](audits/platform-schedule-projection-fix-2026-09-05.md)
- [Individual-step retry recovery](audits/platform-step-retry-recovery-2026-09-05.md)
- [Cross-workflow Pulse platform triage — 2026-08-29](audits/pulse-platform-triage-2026-08-29.md)
- [SQLite platform backlog comparison — 2026-09-05](audits/pulse-platform-triage-2026-09-05.md)
- [Run-mode capability boundary review — 2026-09-06](audits/run-mode-capability-boundary-review-2026-09-06.md)
- [Session identity and tool lifecycle review — 2026-09-07](audits/session-tool-lifecycle-review-2026-09-07.md)
- [Workflow skill consistency review — 2026-09-06](audits/workflow-skill-consistency-2026-09-06.md)

### Core ([index](core/README.md))

- [Azure AI Foundry & Responses API Integration](core/azure_foundry_integration.md)
- [Bot Connector System](core/bot_connector_system.md)
- [Bot Connectors: Architecture, Configuration, and Review](core/bot_connectors_combined.md)
- [Browser Automation](core/browser.md)
- [Coding CLI turn signals](core/coding_cli_turn_signals.md)
- [Native agent tools](design/native_agent_tools.md)
- [Coding CLI updates](core/coding-cli-updates.md)
- [Coding Agent Builder E2E Contract](core/coding_agent_builder_e2e_contract.md)
- [Coding Agent Continuation Architecture](core/coding_agent_continuation_architecture.md)
- [Coding-Agent Timeout Contract](core/coding_agent_timeout_contract.md)
- [Coding CLI onboarding: contract review and implementation map](core/coding_cli_onboarding_contract_review.md)
- [Runloop — Plan](core/electron_standalone_app_plan.md)
- [Environment-Based API Key Defaults](core/env-api-key-defaults.md)
- [Event Cleanup - Progress](core/event_cleanup.md)
- [Event System Architecture](core/event_system.md)
- [Folder Guard System](core/folder_guard_system.md)
- [LLM Configuration & Resilience](core/llm_configuration_and_resilience.md)
- [🌉 MCP Bridge Layer & Exposed APIs](core/mcp_bridge_layer.md)
- [Multi-User Authentication & Workspace Isolation](core/multi_user_authentication.md)
- [Native Workspace Mode](core/native_workspace_mode.md)
- [OAuth Integration Guide](core/oauth.md)
- [Designing a product.yaml](core/product_yaml_design_guide.md)
- [Remote Workspace Gateway + Local Runner Plan](core/remote_workspace_server_plan.md)
- [Session And Tool Binding](core/session_and_tool_binding.md)
- [Skills System](core/skills.md)
- [Slack Connections (Per-Workflow Slack Apps)](core/slack_connections.md)
- [Streaming LLM Output](core/streaming_llm_output.md)
- [Terminal lifecycle](core/terminal_lifecycle.md)
- [WhatsApp Connector](core/whatsapp.md)

### Design

- [Agent tool surface: one source of truth](design/agent_tool_surface_single_source.md)
- [Direct API Transport vs. Routing Through Pi/MCP](design/api_transport_vs_pi_tradeoff.md)
- [Chief of Staff as a standalone product](design/chief_of_staff_as_product.md)
- [Personal Finance Dashboard: a consolidated view across finance workflows](design/finance_dashboard_product.md)
- [Multiple provider accounts for workflows and Crew](design/multiple-provider-accounts.md)
- [Native Coding-Agent Environment Policy](design/native_coding_agent_environment_policy.md)
- [Product API Transport for Coding Agents](design/product_api_transport_for_coding_agents.md)
- [Product Tool Registration and Agent Visibility](design/product_tool_registration_and_visibility.md)
- [Pulse scheduled review lifecycle — historical design spec](design/pulse-post-run-monitor-spec.md)
- [Reusable Platform for Dedicated Agent Products](design/reusable_vertical_product_platform.md)
- [Skill system — current state and narrow product-skill design](design/skill_system_design.md)
- [SparkQuill desktop on the platform — plan and research record](design/sparkquill_desktop_on_platform_plan.md)
- [User accounts, product access, and workflow sharing](design/user_accounts_and_workflow_sharing.md)
- [Video Studio Inside AgentWorks](design/video_studio_inside_agentworks.md)
- [Work Product Design](design/work_product.md)
- [Schedule ownership and Builder warnings](design/workflow-schedule-guard.md)
- [A Workflow as a Product: Custom UI Frontend, AgentWorks as Backend](design/workflow_custom_ui_product.md)

### Getting Started ([index](getting-started/README.md))

- [Connect an AI agent with MCP](getting-started/agentworks-cli-mcp.md)
- [Build Your First Workflow](getting-started/first-workflow.md)
- [Testing workflow changes alongside a running AgentWorks](getting-started/isolated-workflow-testing.md)

### Handover

- [Video Studio handover](handover/video_studio_handover.md)

### Integrations

- [Default productivity MCP connections](integrations/default-productivity-mcps.md)

### Multiagent ([index](multiagent/README.md))

- [Agent Memory System](multiagent/agent_memory_system.md)
- [Multi-Tab Chat Architecture](multiagent/multi_tab_chat_architecture.md)
- [Slash Commands System](multiagent/slash_commands.md)
- [Sub-Agent Delegation System](multiagent/sub_agent_delegation.md)

### Refactor ([index](refactor/README.md))

- [Canonical agent-definition construction](refactor/canonical_agent_definition_construction.md)
- [Refactor spec: unify CLI live-input on tmux-session liveness (drop steer-vs-queue)](refactor/cli_live_input_unification.md)
- [Durable submit acknowledgement: file-ack P0 + pane fast-confirm P1](refactor/durable_ack_p0.md)
- [Lazy Per-Terminal Event Loading](refactor/lazy_per_terminal_event_loading.md)
- [Live-Attach Terminal: App vs PoC Demo — Debug Handoff](refactor/live_attach_app_vs_demo_debug.md)
- [mcpagent public API simplification](refactor/mcpagent_public_api_simplification.md)
- [Native streaming speech-to-text](refactor/native_streaming_stt.md)
- [Design: Live-attach terminal transport (replace snapshot/replay mirror)](refactor/terminal_live_attach_transport.md)

### Workflow ([index](workflow/README.md))

- [Workflow API triggers](workflow/api-triggers.md)
- [Auto-Improvement Framework](workflow/auto_improvement_framework.md)
- [Backup, History & Versions Consolidation](workflow/backup_history_consolidation.md)
- [Browser Automation in Workflows](workflow/browser_automation.md)
- [Cost And Log Measurement](workflow/cost_and_log_measurement.md)
- [Crew workflow step](workflow/crew-step.md)
- [Deterministic Routing (route-by-file)](workflow/deterministic_routing.md)
- [Eval Removal Plan: Migrate to Producer-Owned Measurement](workflow/eval_removal_plan.md)
- [Human Feedback System](workflow/human_feedback_system.md)
- [Iteration Run Folder Architecture](workflow/iteration_run_folder_architecture.md)
- [Learn Code and Code Execution Modes](workflow/learn_code_flow.md)
- [Learning Architecture](workflow/learning_architecture.md)
- [LinkedIn Pulse Review Audit — 2026-08-02](workflow/linkedin_pulse_review_audit_2026-08-02.md)
- [Message Sequence Steps](workflow/message_sequence_step_design.md)
- [Orchestrator Step Type](workflow/orchestrator-step-type.md)
- [Org Dashboard — design](workflow/org_dashboard_design.md)
- [Persistent Stores Design](workflow/persistent_stores_design.md)
- [Deterministic Pre-Validation Guide](workflow/pre_validation_guide.md)
- [Publish — share a workflow's HTML to a public URL](workflow/publish_design.md)
- [Pulse: current architecture and decision map](workflow/pulse_consolidation.md)
- [Pulse v2.1: Reliability-First Experiment Proposal](workflow/pulse_v2_1_experiment_proposal.md)
- [Pulse v2: Proof-Carrying Workflows and Exception-Driven Autonomy](workflow/pulse_v2_proof_carrying_architecture.md)
- [Workflow self-improvement & reporting — system overview](workflow/self_improvement_and_reporting.md)
- [Shared knowledge bases through workflow references](workflow/shared_knowledgebase_sources.md)
- [step_config.json Format Specification](workflow/step_config_format_specification.md)
- [Tiered LLM Allocation](workflow/tiered_llm_allocation.md)
- [Tool Filtering System](workflow/tool_filtering_system.md)
- [Workflow Builder Commands And Tools](workflow/workflow_builder_commands_and_tools.md)
- [Interactive Workflow Builder](workflow/workflow_builder_interactive.md)
- [Workflow Manifest Architecture](workflow/workflow_manifest_architecture.md)
- [Workflow Monitoring](workflow/workflow_monitoring.md)
- [Workflow Scheduling](workflow/workflow_scheduling.md)
- [Workflow Shell Working Directory](workflow/workflow_shell_working_directory.md)

### Bugs ([index](bugs/README.md))

Incident archive — 350+ investigations, browsed via its index, not listed here.

## Validate Links

Run the documentation link check before merging documentation changes:

```bash
node scripts/check-doc-links.js
```

## Isolated workflow testing

Use the [standard isolated workflow testing process](getting-started/isolated-workflow-testing.md) to reproduce platform issues with a disposable copy of `Workflow/testing` while the local AgentWorks app keeps running.
