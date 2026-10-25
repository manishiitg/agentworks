## Gmail and Google Workspace

- Determine the mailbox from `list_gmail_connections` and a successful scoped
  account check. A Claude/Codex/other provider login or billing email is not the
  AgentWorks user or a Gmail connection. The signed-in app email also does not
  prove Gmail consent. If no connection is returned, say no account is connected;
  ask which account the user wants to connect without suggesting a provider email.

- Gmail uses saved Google accounts in **Integrations → Google apps → Connect or manage accounts**,
  selected with **Use here** in this project’s **Integrations → Google apps** (or Recent);
  it is not an MCP server. Company incoming email is stored in this agent’s dedicated Inbox and optional normal Python Watchers start work. Personal Gmail connects through gog. An explicitly requested personal Gmail Watcher can pass selected new mail to the owner’s Crew chat; it never fills the shared Inbox. Check `list_gmail_connections`
  before searching the MCP catalog or claiming Gmail is unavailable.
- Before calling `google_workspace_cli`, use `list_skills` to check whether the
  upstream `gog` skills are installed. If they are missing, call
  `install_skill(source="https://github.com/openclaw/gogcli")`. Do this only
  when Google Workspace work needs the CLI guidance; opening a project project
  must not install external software or skills automatically.
- Use `read_skill` to load `gog` and the relevant installed `gog-gmail`,
  `gog-drive`, `gog-sheets`, `gog-docs`, `gog-slides`, or `gog-calendar`
  skill. Those versioned skills define current commands; do not infer syntax
  from old examples or retry by guessing.
- Pass the skill's command arguments to `google_workspace_cli` without the
  `gog` binary, account, client, home, or credential flags. The server selects
  the requested connection and injects its credential privately.
  Never request, print, or copy credential environment variables into chat or
  project files.
- Mailbox access requires both `allow_read_access` and an observed Google
  `gmail.readonly` grant. If requested and granted state differ, use
  `update_gmail_connection_grants` and tell the user to complete the returned
  reconnect flow. Do not install a Gmail MCP server as a workaround.
- Agent draft creation and send/reply require both
  `allow_agent_write_access` and an observed Google `gmail.compose` grant.
  This is off by default and separate from workflow run summaries; never remove the
  Gmail send guard or treat `gmail.send` alone as agent-write permission.
- Gmail notifications and agent-authored sends are outbound operations.
  Gmail accounts are managed in Integrations → Google apps using gog. On servers, choose one account with Use for Crew inboxes to supply your Crew addresses; receiving is on by default per Crew and its owner can turn it off. Read get_agent_inbox and use normal Python Watchers for actions.
