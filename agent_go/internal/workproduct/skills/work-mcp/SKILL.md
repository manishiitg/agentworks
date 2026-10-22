---
name: work-mcp
description: Connect and manage this project's MCP connections for {{product}} projects.
---

# {{product}} MCP

Read this skill before acting on an MCP Connect request. Use the setup tools
below; a new connection request does not require reading old conversations.

Use `list_mcp_servers` to inspect this project's connections. Use `search_mcp_catalog` for connection templates.

- A new MCP connection belongs to the project it is added to: everyone with
  access to the project uses it, and no other project does. It acts as the
  connected account, so say whose login it is. Use `install_mcp_server` for a
  catalog server. Crew agent turns, including function and message runs, can
  install or reuse current catalog servers and verified public remote MCP
  URLs through the advertised setup tool. Keep its URL and ownership checks;
  never attach another person's saved connection or bypass a denied call.
<!-- product:mcp-gateway -->- MCPs shared across projects belong in **Vault**. An administrator connects them there and
  assigns tools and resource conditions to groups. Connecting grants no access.
- Use `update_project_mcp_server_selection` to select or deselect a
  private connection for the active project. Vault MCPs are available automatically
  through current user/group permissions; project selection does not limit them.
- Vault checks the caller's current permissions on every call, including
  argument conditions and schema validation. Revoked permissions
  stop working on retained sessions too. Never suggest a direct upstream URL
  or another user's connection as a way around a denied call.
<!-- /product -->- Use `trigger_mcp_discovery` to refresh an owned or permitted connection.
  Discover loaded tools with `search_tools(query="<provider or task>")`, then
  `get_api_spec(tool_name="<returned-name>")` for their argument schemas.
  Only use a runtime `server_name` returned by `search_tools` when filtering;
  public connection IDs and connection names are selection IDs.
- Tools become available from the next user message. Confirm what was saved,
  which connection was saved and any remaining sign-in step.
- For a Connect request, inspect the current inventory first to avoid a duplicate.
  Install the requested catalog connection, return any real OAuth sign-in link,
  and select the connection for this project after sign-in. Refresh the inventory
  to verify `connected`; an installation or sign-in link alone is not success.
  Completing that setup and checking it are part of the user's Connect request;
  do not ask again whether to check or select the requested connection.
- If a setup or shell tool fails, report the observed error briefly. Do not send
  the person back to the same Connect button that invoked this request, retry
  identical failing commands, or describe a filesystem error as a missing role.
- Own the setup in chat instead of sending the person to a settings screen.
  For a user-provided API key, pass `api_key` to `install_mcp_server` so the
  server seals it; use the provider's documented `api_key_header` and
  `api_key_format` when its header is not Authorization/Bearer. For OAuth app
  setup, request only the missing `client_id` and `client_secret`, then call
  the same tool on the saved exact connection name without label or catalog.
  Credentials are encrypted; never repeat values or write them to project
  files. Never ask for the person's account password. Display only the real
  returned sign-in URL; the person signs in and approves at the provider.

## Help the person reach the current screen

The menu has Recent, Build, Use, Share, Integrations and Setup. An item in
Recent is not repeated in its category. MCP connections are labelled **MCPs**;
**MCP access** is a different page for using AgentWorks from another AI app.
There is no separate "Integrations > Connections > Available" menu path.

Connecting is done through the setup tools in chat. Only when the person asks
to inspect the connection or needs help finding a screen, use UI navigation.
In an interactive Builder, discover the UI tools as described in
`work-ui-control`, inspect `list_ui_capabilities`, then open the advertised
`mcp` view with target `connected` to show saved and pending accounts. Use
target `available` only to add a new provider. Confirm an `applied` receipt
before saying the page opened. Refresh it after setup if it is already open.
If UI control is unavailable, say **Ctrl+K** (**Cmd+K** on Mac), search
**MCPs**, and select the current project's result. The menu's **Find a view**
opens the same search. Do not invent a sidebar or ask the person to hunt for
a retired tab.

If setup returns no sign-in link, explain the actual tool result. A provider
that requires an OAuth app needs guided steps at the provider and the exact
callback URL returned by the tool; collect its app details and retry the saved
name here. A missing server callback address is a server configuration problem,
not a reason to send the person to AgentWorks settings. Do not create another
account, invent a link, or claim it connected. After authorization, verify and
finish the original request without asking again whether to continue.

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
group login.
<!-- product:mcp-gateway -->
Vault MCPs are available automatically through the executing user's current user/group permissions, independent of project MCP selections. Do not call select/deselect for Vault. Tool grants and regex rules are checked at the gateway on every call. Secrets still require explicit selection by name.
<!-- /product -->
