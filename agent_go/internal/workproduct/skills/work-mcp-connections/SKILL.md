---
name: work-mcp-connections
description: Use and connect this {{product}}'s MCP connections (Linear, Asana, Notion, Figma, ...). Use when the user asks to read email, use a connected service, connect one, or when an MCP tool is missing or fails.
---

# {{product}} MCP

Connections are private to the authenticated person, even in a shared {{product}}.
Shared MCPs belong in Vault and require group permissions on every tool call.

- **See what is connected.** Call `manage_my_mcp_servers` with `list`:
  `this_code_has` names the connections and whether each is `connected`;
  `catalog` is what can be added; `you_can_connect` says whether this person
  may. `vault` lists shared connections permitted by their groups; use
  `manage_my_mcp_servers(action="select", server="<vault name>")` to select one
  for this Code, or `deselect` to remove it from the selection. First call `search_tools(query="<provider or task>")` to find registered tools. Use the exact runtime `server_name` returned by that search to narrow subsequent searches, then `get_api_spec(tool_name="<returned-name>")` for a schema. Private names and Vault selection IDs from the connection list are not runtime server names.
- **Server names.** Connected servers appear as `u<id>__<name>`, for example
  `u3f2a...__googlegmail`. Use that exact name in tool calls. When you talk to
  the person, say "your Gmail connection" and never show the `u<id>__` id.
- **After connecting.** Refresh connection state with `list`. Newly connected
  tools become registered on the next message. Use `search_tools` for the
  registered tools; a connected entry alone is not proof its tools are loaded.
  Report an observed discovery or connection failure instead of guessing names.
- **Connect one.** `manage_my_mcp_servers` with `connect` and `catalog` (for
  example `GoogleGmail`), or `name` and `url` for a server that is not in the
  catalog. It returns a sign-in link for the person to open with their own
  account. Only this {{product}}'s owner can connect; anyone else can list.
  Google apps (Gmail, Drive, Calendar, Docs, Sheets, Slides) and GitHub are
  not MCP connections: Google is the **Google apps** tab (the person signs in
  there and you use it through `google_workspace_cli`); GitHub is a personal
  access token in the secret `GITHUB_TOKEN`, used with git and the GitHub API.
  Point the person there instead of trying `connect`.
- **Providers that need an OAuth app.** When `connect` says so, send the person
  to **Integrations > MCP** to finish it. Never ask for passwords, API keys
  or OAuth client secrets in chat. A server that takes an API key uses a secret
  the person stores privately; a connection refers to its secret name.
- **Remove one.** `manage_my_mcp_servers` with `remove` and the connection's
  `name` detaches it from this project; their private account can be reused elsewhere. Say which connection you are removing first.
- **When a call fails.** "not available in this chat" means the connection was
  removed or its owner lost access. Run `list`, then connect it again. A
  direct tool that is missing is not a failure: use the bridge.
