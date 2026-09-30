---
name: work-mcp
description: Connect and manage MCP servers for {{product}} projects. Use when the user asks to connect a service through MCP, inspect connection or authorization state, select MCP access for a project, refresh tool discovery, or diagnose an MCP server.
---

# {{product}} MCP

Inspect the current MCP state before changing it, and distinguish platform-level
connection setup from selection for this project.

- Use `list_mcp_servers` to inspect installed connection and authorization
  state. Use `search_mcp_catalog` only to discover a new server.
- Use `install_mcp_server` for a catalog result or service URL so {{product}} can probe
  its authentication requirements. Use `add_mcp_server` only for a custom
  server whose protocol and complete configuration are already known.
- An installed server is a platform connection shared by AgentWorks, Crew, Code,
  workflows, chats, schedules, and every user. Only a platform administrator
  may add, authenticate, reconnect, edit, or remove one. Explain this before
  starting credential or OAuth setup. A connection is not automatically
  selected for this project.
- When the user asks to use an already-connected server in the active {{product}}
  project, call `update_project_mcp_server_selection` with `action: select`.
  The selection is written to this project's `workflow.json`; its tools become
  available on the next user message because the current turn retains its
  original MCP scope. Do not tell the user to open Setup when this tool can do
  the selection. Use `action: deselect` when the user asks to remove project
  access. The same selection remains editable in **Setup > MCP servers**.
- **Connections with a person's login.** A project can also have its own
  connections added with someone's own login (their Gmail, Drive, GitHub, ...).
  Everyone using the project uses them as that person, and they belong to this
  project only. They are not platform connections, so no admin is needed: anyone
  who can edit the project connects one under **MCP connections > Available**
  (search, then **Connect**) and signs in there. You cannot add or sign in to
  one for them. Point them to that list when they want their own account and
  no platform connection fits. They appear as servers named `u<id>__<name>` and
  are already on for this project: never select or deselect them.
- Never claim server tools are available until the server is connected and
  selected. After selecting, clearly state the next-message boundary.
- Use `get_mcp_server_logs` to diagnose a configured server and
  `trigger_mcp_discovery` when its tool metadata is stale. Removing a server is
  platform-wide, so identify the exact server and explain that scope first.

## Multiple accounts

Use the exact connection IDs from `list_mcp_servers`. Distinct OAuth accounts
remain separate, for example `Linear-base` and `Linear`; existing selections
keep their original account. Never infer workspace identity from the connection
name or credential location. Verify it using that connection's read-only team or
identity tools. If tools are not loaded, select the explicit connection and verify
on the next user message. When account choice is unclear, show the IDs and ask
which to use. Never expose tokens, credential paths, headers or environment values.
