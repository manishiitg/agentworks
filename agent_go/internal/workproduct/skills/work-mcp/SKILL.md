---
name: work-mcp
description: Connect and manage private MCP servers and select permitted Vault connections for {{product}} projects.
---

# {{product}} MCP

Read this skill before acting on an MCP Connect request. Use the setup tools
below; a new connection request does not require reading old conversations.

Use `list_mcp_servers` to inspect the caller's private connections and the Vault
servers their groups permit. Use `search_mcp_catalog` for connection templates.

- New MCP connections are private to the authenticated user. Use
  `install_mcp_server` for a catalog server or a user-supplied remote MCP URL.
  Other users of the same project cannot run with that person's credentials.
- Shared MCPs belong in **Vault**. An administrator connects them there and
  assigns tools and resource conditions to groups. Connecting grants no access.
- Use `update_project_mcp_server_selection` to select or deselect a private
  connection or an exact Vault connection ID for the active project. Project
  selection is an additional limit; it cannot grant Vault permissions.
- Vault checks the caller's current permissions on every call, including
  argument conditions, schema validation and PII rules. Revoked permissions
  stop working on retained sessions too. Never suggest a direct upstream URL
  or another user's connection as a way around a denied call.
- Use `trigger_mcp_discovery` to refresh an owned or permitted connection.
  Discover loaded tools with `search_tools(query="<provider or task>")`, then
  `get_api_spec(tool_name="<returned-name>")` for their argument schemas.
  Only use a runtime `server_name` returned by `search_tools` when filtering;
  public connection IDs and private connection names are selection IDs.
- Tools become available from the next user message. Confirm what was saved,
  which connection is private or shared, and any remaining sign-in step.
- For a Connect request, inspect the current inventory first to avoid a duplicate.
  Install the requested catalog connection, return any real OAuth sign-in link,
  and select the connection for this project after sign-in. Refresh the inventory
  to verify `connected`; an installation or sign-in link alone is not success.
- If a setup or shell tool fails, report the observed error briefly. Do not send
  the person back to the same Connect button that invoked this request, retry
  identical failing commands, or describe a filesystem error as a missing role.
- Never ask for passwords, API keys or OAuth client secrets in chat. Direct
  users to **Integrations > My MCPs** for secure credential entry. Display
  OAuth links only when the setup tool actually returns one.
