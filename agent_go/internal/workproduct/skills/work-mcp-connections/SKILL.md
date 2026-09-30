---
name: work-mcp-connections
description: Use and connect this {{product}}'s MCP connections (Linear, Asana, Notion, Figma, ...). Use when the user asks to read email, use a connected service, connect one, or when an MCP tool is missing or fails.
---

# {{product}} MCP

A {{product}} has its own MCP connections, added with its owner's own sign-in
(their Linear, Notion, ...). Every chat here uses them as that person,
schedules, triggers and bots included. They belong to this {{product}} only and
never appear in another one.

- **See what is connected.** Call `manage_my_mcp_servers` with `list`:
  `this_code_has` names the connections and whether each is `connected`;
  `catalog` is what can be added; `you_can_connect` says whether this person
  may. Use `get_api_spec` to see a connected server's tools.
- **Server names.** Connected servers appear as `u<id>__<name>`, for example
  `u3f2a...__googlegmail`. Use that exact name in tool calls. When you talk to
  the person, say "your Gmail connection" and never show the `u<id>__` id.
- **A connected service works right away through the API bridge.** The bridge
  looks the connection up on every call, so a service the person just signed in
  to is usable at once, even when its tools are not in your direct tool list
  (that list is fixed when the chat starts). Never conclude "its tools are not
  loaded, so I cannot"; run `manage_my_mcp_servers` with `list`, then
  `get_api_spec` for the server and call it through the bridge. Only say a
  service is unavailable when it is not in `list` or is not `connected`, and then
  connect it first.
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
  the owner adds under **Setup > Secrets**, and the connection names it.
- **Remove one.** `manage_my_mcp_servers` with `remove` and the connection's
  `name` deletes it and its login. Say which connection you are removing first.
- **When a call fails.** "not available in this chat" means the connection was
  removed or its owner lost access. Run `list`, then connect it again. A
  direct tool that is missing is not a failure: use the bridge.
