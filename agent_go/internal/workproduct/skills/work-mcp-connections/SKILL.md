---
name: work-mcp-connections
description: Use and connect this {{product}}'s MCP connections (Gmail, Drive, GitHub, Linear, ...). Use when the user asks to read email, use a connected service, connect one, or when an MCP tool is missing or fails.
---

# {{product}} MCP

A {{product}} has its own MCP connections, added with its owner's own sign-in
(their Gmail, Drive, GitHub, ...). Every chat here uses them as that person,
schedules, triggers and bots included. They belong to this {{product}} only and
never appear in another one.

- **See what is connected.** Call `manage_my_mcp_servers` with `list`:
  `this_code_has` names the connections and whether each is `connected`;
  `catalog` is what can be added; `you_can_connect` says whether this person
  may. Use `get_api_spec` to see a connected server's tools.
- **Server names.** Connected servers appear as `u<id>__<name>`, for example
  `u3f2a...__googlegmail`. Use that exact name in tool calls. When you talk to
  the person, say "your Gmail connection" and never show the `u<id>__` id.
- **Never say a service is available until it is.** If it is not in `list`, or
  is not `connected`, connect it first. Its tools are available from the
  person's next message after they sign in: the current turn keeps the tool set
  it started with, so say so plainly instead of retrying.
- **Connect one.** `manage_my_mcp_servers` with `connect` and `catalog` (for
  example `GoogleGmail`), or `name` and `url` for a server that is not in the
  catalog. It returns a sign-in link for the person to open with their own
  account. Only this {{product}}'s owner can connect; anyone else can list.
  One Google sign-in covers Gmail, Drive, Calendar and the other Google
  services: connect each one you need, the person signs in once.
- **Sign-in apps.** Providers such as Google and GitHub need an OAuth app. When
  `connect` says so, send the person to **Integrations > MCP** to finish it (an
  admin sets the app up once for everyone). Never ask for passwords, API keys
  or OAuth client secrets in chat. A server that takes an API key uses a secret
  the owner adds under **Setup > Secrets**, and the connection names it.
- **Remove one.** `manage_my_mcp_servers` with `remove` and the connection's
  `name` deletes it and its login. Say which connection you are removing first.
- **When a call fails.** "not available in this chat" or "not registered by any
  connected server" means the connection was removed, its owner lost access, or
  it was added after this turn began. Run `list`, then connect it again or ask
  the person to send another message.
