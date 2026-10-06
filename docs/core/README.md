# Core Docs

These docs describe platform subsystems that cut across workflow and multi-agent mode.

## Includes

- Auth, secrets, user isolation, and policy controls
- Eventing, streaming, session propagation, and runtime plumbing
- LLM/provider configuration and integration docs
- Browser, connector, MCP bridge, and platform plans

## Files

- `azure_foundry_integration.md`
- `bot_connector_system.md`
- [Combined bot connector guide and review](bot_connectors_combined.md): current architecture, configuration, Slack setup, lifecycle, and verified documentation gaps.
- `browser.md`
- [Coding CLI turn signals](coding_cli_turn_signals.md): where each CLI's intake, completion and final answer come from; steering; background subagents.
- [Coding CLI updates](coding-cli-updates.md): daily checks, persisted status, and per-chat release selection.
- `coding_agent_builder_e2e_contract.md`
- [Coding CLI onboarding contract review](coding_cli_onboarding_contract_review.md): implementation map, P0/P1 enforcement findings, new-provider checklist, and proposed registration consolidation.
- `coding_agent_continuation_architecture.md`
- `electron_standalone_app_plan.md`
- `env-api-key-defaults.md`
- `event_cleanup.md`
- `event_system.md`
- `folder_guard_system.md`
- `llm_configuration_and_resilience.md`
- `mcp_bridge_layer.md`
- `multi_user_authentication.md`
- `native_workspace_mode.md`
- `oauth.md`
- [Local and Server Agent Design](remote_workspace_server_plan.md): public MCP with guarded writes for local → server; an authenticated laptop executor for server → local.
- [Previous workspace-router proposal](remote_workspace_server_plan.html) (superseded).
- `session_and_tool_binding.md`
- `secrets.md`
- `skills.md`
- `streaming_llm_output.md`
