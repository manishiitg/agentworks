---
name: panel-mcp-connections
description: MCP connections panel in Goals and Crews. Select, connect, inspect or remove external tools used by
  the project.
metadata:
  panel-tools:
    work:
    - list_mcp_servers
    - search_mcp_catalog
    - install_mcp_server
    - add_mcp_server
    - remove_mcp_server
    - get_mcp_server_logs
    - trigger_mcp_discovery
    - update_project_mcp_server_selection
    - settings
    agentworks:
    - list_mcp_servers
    - search_mcp_catalog
    - install_mcp_server
    - add_mcp_server
    - edit_mcp_server
    - remove_mcp_server
    - get_mcp_server_logs
    - trigger_mcp_discovery
    - settings
    relays:
    - list_mcp_servers
    - search_mcp_catalog
    - install_mcp_server
    - add_mcp_server
    - edit_mcp_server
    - remove_mcp_server
    - trigger_mcp_discovery
    - settings
  external-tools:
  - crew_mcp
---

# MCP connections

In Crew and Goal chats these resources live together in the Vault panel.
Use panel-vault for the panel inventory, project-use ticks and chat routing;
this procedure covers the underlying operation.

Start with list_mcp_servers and current selection. Use the admitted MCP discovery/install/add/edit/remove tools for a connection and update_project_mcp_server_selection in Crew or settings in Goals for project selection. Shared Vault access uses existing Vault tools and permissions. Reuse a saved connection; sign-in may still be a person action. Verify selection and tool availability, not just that a panel opened. Never hand-edit managed MCP configuration.

For Vault sign-in, discovery, selection and connection setup from chat, Read [the detailed procedure](references/connections.md).
