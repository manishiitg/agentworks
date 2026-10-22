---
name: work-mcp
description: Connect and use apps and MCP tools through Vault for {{product}}.
---

# Connect apps through Vault

Vault is the only MCP account and credential store. Projects select Vault
connections; they never keep a second login. A project uses its owner's selected
accounts under the existing sharing, Builder/Run and Vault rules. Personal Slack
keeps its send-only restriction for other people. Never widen grants to bypass a
refusal or connect another person's account without its existing Vault grant.

## Complete setup in chat

1. Inspect `list_mcp_servers` for the live permitted inventory, owned vaults and
   catalog. Reuse the requested account. Use `search_mcp_catalog` when necessary;
   registry entries are leads, so verify custom endpoints against provider docs.
2. Use `install_mcp_server` with a catalog provider, or name and verified URL.
   A label creates a separate named account. Reconnect using the exact saved
   `vault_<connection ID>`, omitting label/catalog so it does not create a duplicate.
   An optional `vault_id` selects a vault the person already owns.
3. For a user-supplied API key, pass `api_key`, with `api_key_header` and
   `api_key_format` when required. For OAuth, return the real sign-in URL. The
   person signs in and approves at the provider; never ask for their password.
4. If the provider requires an OAuth app, guide its creation here using the
   returned callback URL. Collect only the missing `client_id`/`client_secret`,
   then retry the exact saved account. Never invent a link or send the person to
   a retired Integrations > Connections page or secure credential form.
5. Run `trigger_mcp_discovery` on that exact account after approval, inspect the
   real result and finish the requested project selection. Setup, verification
   and selection are already authorized by a request to connect an app here.
   A saved account or sign-in link alone is not a working connection.

Credentials are encrypted by Vault. Never repeat their values, write them in
project files, print them in shell output, or include them in URLs. A value the
person supplies may stay in their chat history. Tool results contain metadata,
not credential values. Report actual errors in plain words and continue the
setup the tools support; do not send users back to the button that opened chat.

## Use and selection

Use the product's advertised selection tool (`update_project_mcp_server_selection`
for Crew, the settings/config tool for Goals and Flows). Select exact Vault IDs.
No explicit picks means all currently permitted Vault accounts; an explicit
NO_SERVERS selection means none. Removing a connection from a project only
changes selection. Removing it from Vault removes the account everywhere.
Vault grants, tool permissions and argument conditions are checked at every call.

Use `search_tools` for the task and `get_api_spec` for the returned tool. Runtime
server names can contain a session suffix: use the exact name returned by search,
not an invented alias. New tools become available on the next message. Inspect
discovery before claiming a tool is loaded. Labels are user labels, not verified
provider identities.

For account-level actions, `manage_my_vaults` manages only vaults the person owns:
connect/sign_in, sync/remove_connection, set_secret/remove_secret and membership.
Administrators use `manage_vault_access` for company connections and grants;
`sign_in_apps` and `configure_sign_in_app` manage shared provider app registrations.
Do not change memberships or grant tools merely to finish a connection.

## Screen help only when requested

The project menu is Recent / Build / Use / Share / Integrations / Setup. Recent
items are not repeated below. MCPs shows Vault accounts and selection; Secrets
shows Vault-backed secret names. MCP access connects another AI app to AgentWorks.
Use the advertised UI tool and require an applied receipt before claiming it
opened. Otherwise suggest Ctrl+K (Cmd+K on Mac), search MCPs or Secrets, and pick
the current project's result. Setup itself stays in chat.
