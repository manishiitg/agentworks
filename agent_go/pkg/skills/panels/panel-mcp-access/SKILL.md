---
name: panel-mcp-access
description: MCP access panel in Goals and Crews. Help the person connect an external agent to this project.
metadata:
  panel-tools:
    work:
    - manage_my_vaults
    - settings
    - get_ui_state
    - perform_ui_action
    agentworks:
    - manage_my_vaults
    - settings
    - get_ui_state
    - perform_ui_action
    relays:
    - settings
    - get_ui_state
    - perform_ui_action
  external-tools: []
---

# MCP access

Use the current project’s admitted MCP/access setup tools to inspect the connection and return the server’s setup instructions. These are clients reaching AgentWorks, distinct from MCP services used by the project. Never create a duplicate service connection, invent a token, or paste a secret value. The person completes any sign-in. Verify the authorized connection state through the same backend.
