---
name: panel-access
description: Share a product, select accounts, secrets and connections, or contact
  agents. Use existing access and keep personal information private.
metadata:
  version: 2.0.0
  panel-tools:
    work:
    - share
    - crew_owners
    - manage_my_vaults
    - settings
    - get_ui_state
    - perform_ui_action
    - manage_google_account
    - manage_slack_account
    - manage_laptop_access
    - list_secrets
    - set_workflow_secret
    - delete_workflow_secret
    - update_project_global_secret_selection
    - list_mcp_servers
    - search_mcp_catalog
    - install_mcp_server
    - add_mcp_server
    - remove_mcp_server
    - get_mcp_server_logs
    - trigger_mcp_discovery
    - update_project_mcp_server_selection
    - list_agents
    - send_message
    - read_agent_messages
    - files
    - list_accessible_workflows
    - list_attached_workflows
    - database
    - attach_workflow_reference
    - detach_workflow_reference
    - list_needs_you
    - answer_needs_you
    - dismiss_needs_you
    agentworks:
    - share
    - manage_project
    - workflow_team
    - get_workflow_team
    - manage_my_vaults
    - settings
    - get_ui_state
    - perform_ui_action
    - manage_google_account
    - manage_slack_account
    - list_secrets
    - set_workflow_secret
    - delete_workflow_secret
    - list_mcp_servers
    - search_mcp_catalog
    - install_mcp_server
    - add_mcp_server
    - edit_mcp_server
    - remove_mcp_server
    - get_mcp_server_logs
    - trigger_mcp_discovery
    - list_agents
    - send_message
    - read_agent_messages
    - list_accessible_workflows
    - files
    - get_human_input_request
    - answer_human_input_request
    - create_human_input_request
    - withdraw_human_input_request
    relays:
    - share
    - manage_project
    - settings
    - get_ui_state
    - perform_ui_action
    - list_secrets
    - set_workflow_secret
    - delete_workflow_secret
    - list_mcp_servers
    - search_mcp_catalog
    - install_mcp_server
    - add_mcp_server
    - edit_mcp_server
    - remove_mcp_server
    - trigger_mcp_discovery
    - list_agents
    - send_message
    - read_agent_messages
    - files
  external-tools:
  - share
  - workspace
  - relay_owners
  - settings
  - integrations
  - terminal
  - settings.secret_sources
  - mcp
  - messages
  - files
  - needs_you
---

# Access

Share a product, select accounts, secrets and connections, or contact agents. Use existing access and keep personal information private.

Start with the person's request and the current product. Read only the relevant procedure below: in product chat use `read_skill` with this skill name as `skill_name` and the listed `path` (chat has no `help action=topic`); only an MCP client uses `help action=topic` with this name as `topic` and the listed `path`. Use the actions offered by the current tool catalog. A guide does not grant access.

- Sharing: read `references/sharing/SKILL.md` when needed.
- Owners: read `references/owners/SKILL.md` when needed.
- Team: read `references/team/SKILL.md` when needed.
- MCP access: read `references/mcp-access/SKILL.md` when needed.
- Vault: read `references/vault/SKILL.md` when needed.
- Secrets: read `references/secrets/SKILL.md` when needed.
- MCP connections: read `references/mcp-connections/SKILL.md` when needed.
- Bullpen: read `references/agentbridge/SKILL.md` when needed.
- Linked folders: read `references/linked-folders/SKILL.md` when needed.
- Suggestions: read `references/suggestions/SKILL.md` when needed.
- Decisions: read `references/decisions/SKILL.md` when needed.

Verify the saved result or returned run result, then give the person one clear next step. Keep their existing notes and omitted settings.
