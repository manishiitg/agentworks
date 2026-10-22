---
name: work-mcp-connections
description: Inspect, connect and use this {{product}}'s Vault MCP accounts.
---

# {{product}} connections

Read `work-mcp` for the full chat setup procedure. Vault is the only MCP account
store. Inspect `list_mcp_servers`; use the exact saved `vault_<connection ID>`.
Never create a project-local account, guess an old u<id> name, or send the person
to Integrations > Connections. Set up API keys and OAuth app details in chat,
return the real provider sign-in link, verify discovery, and finish selection.
Never echo credentials or change unrelated accounts, sharing or grants.

`search_tools` finds current tools; `get_api_spec` gives their schema. Use exact
runtime names returned by search. Existing project selection, owner-lent account
rules, Vault groups, tool permissions and argument conditions still apply.

Google's dedicated saved accounts use `manage_google_account` and
`google_workspace_cli`; reuse those accounts rather than creating a duplicate MCP
unless the person explicitly requests MCP. Slack uses its existing Vault account
through `manage_slack_account`. Only that Slack account's owner may read/search;
other people may use the permitted send-side tools.
