---
name: work-mcp
description: Connect and manage this project's MCP connections and use automatically available Vault connections for {{product}} projects.
---

# {{product}} MCP

Read this skill before acting on an MCP Connect request. Use the setup tools
below; a new connection request does not require reading old conversations.

Use `list_mcp_servers` to inspect this project's connections and the Vault
servers their groups permit. Use `search_mcp_catalog` for connection templates.

- A new MCP connection belongs to the project it is added to: everyone with
  access to the project uses it, and no other project does. It acts as the
  connected account, so say whose login it is. Use `install_mcp_server` for a
  catalog server or a user-supplied remote MCP URL.
- MCPs shared across projects belong in **Vault**. An administrator connects them there and
  assigns tools and resource conditions to groups. Connecting grants no access.
- Use `update_project_mcp_server_selection` to select or deselect a
  private connection for the active project. Vault MCPs are available automatically
  through current user/group permissions; project selection does not limit them.
- Vault checks the caller's current permissions on every call, including
  argument conditions and schema validation. Revoked permissions
  stop working on retained sessions too. Never suggest a direct upstream URL
  or another user's connection as a way around a denied call.
- Use `trigger_mcp_discovery` to refresh an owned or permitted connection.
  Discover loaded tools with `search_tools(query="<provider or task>")`, then
  `get_api_spec(tool_name="<returned-name>")` for their argument schemas.
  Only use a runtime `server_name` returned by `search_tools` when filtering;
  public connection IDs and connection names are selection IDs.
- Tools become available from the next user message. Confirm what was saved,
  which connection belongs to this project or is shared through Vault, and any remaining sign-in step.
- For a Connect request, inspect the current inventory first to avoid a duplicate.
  Install the requested catalog connection, return any real OAuth sign-in link,
  and select the connection for this project after sign-in. Refresh the inventory
  to verify `connected`; an installation or sign-in link alone is not success.
- If a setup or shell tool fails, report the observed error briefly. Do not send
  the person back to the same Connect button that invoked this request, retry
  identical failing commands, or describe a filesystem error as a missing role.
- Never ask for passwords, API keys or OAuth client secrets in chat. Direct
  users to **Integrations** for secure credential entry. Display
  OAuth links only when the setup tool actually returns one.

## Multiple accounts

All products support multiple named connections to one MCP provider.
Use `install_mcp_server(name="<catalog provider>", catalog="<catalog provider>",
label="Notion · Engineering")` to create a separate account. A label creates a
new connection, with a stable generated connection name and independent OAuth
credentials. Return that exact name with any real sign-in link. For another
account, pass a different label and authorize the intended upstream account.
Labels describe the user's choice, not a verified provider identity.

Inspect `list_mcp_servers` first. Reconnect, remove, discover and select using the
exact connection name, not the catalog provider when there are multiple accounts.
To reconnect an existing account, omit label. Never replace another account or
change its selection. Named connections do not share the legacy Google/provider
group login. Code's `manage_my_mcp_servers` supports the same `catalog` and `label`
arguments with `action="connect"`, and exact `name` for an existing connection.
Select each returned connection separately for its project. Shared connections
continue to be installed in Vault and governed through group grants.

Vault MCPs are available automatically through the executing user's current user/group permissions, independent of project MCP selections. Do not call select/deselect for Vault. Tool grants and regex rules are checked at the gateway on every call. Secrets still require explicit selection by name.
