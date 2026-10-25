---
name: panel-vault
description: Familiar Integrations panels for MCPs, Secrets, Google apps, Slack and Local laptop. Connect, repair and share through their existing native tools.
metadata:
  panel-tools:
    work: [manage_my_vaults, settings, manage_google_account, manage_slack_account, manage_laptop_access, list_secrets, set_workflow_secret, delete_workflow_secret, update_project_global_secret_selection]
    agentworks: [manage_my_vaults, settings, manage_google_account, manage_slack_account, list_secrets, set_workflow_secret, delete_workflow_secret]
  external-tools: [get_settings, update_settings, workflow_google_account, workflow_slack_account, manage_laptop_access]
---

# Integrations beside this chat

Use familiar product menu entries: MCPs, Secrets, Google apps, Slack account,
Slack bot and Local laptop. Each opens its native panel directly in the current
Crew or Goal workspace; Flows retain their existing exclusions. Vault remains
the backend account/secret store, not a destination people need to learn.
There is no My Vault page, tab or menu entry.

Use admitted perform_ui_action with view=mcp, action=open and target=connected,
available, secrets, gmail, slack-account or laptop. Slack bot uses the existing
slack-bot target for Crew and slack for Goal. Opening changes presentation only.

MCPs includes its tool lists, catalog and account setup in chat. Secrets shows one list of project and shared names with their source,
plus project add/replace/remove controls; shared values stay hidden and project_select owns the ticks.
Add and Sharing use the panel header, alongside contextual Ask AI. There is no second account/sharing list below MCPs or Secrets. Sharing is managed from the visible chat with existing tools. Google/Slack sign-in, reconnection
and permissions are under that provider’s Connect or manage accounts. Company
OAuth setup remains administrator-only on that provider panel. Slack bot keeps
its bot editor, setup/test controls and administrator-only company bot. Local
laptop includes connection instructions and project folder/command settings.
No connection selection, grant or execution rule changes with this navigation.

Start with settings action=get for this project, or manage_my_vaults
operation=project with its workspace_path. The names-only vault snapshot reports
actual selections, sharing counts and connection status. Never infer a secret
value or credentials from this inventory.

Company Google accounts also appear here using the same project-scoped list as
list_gmail_connections. Their ticks show the existing default account and cannot
be changed with personal-account attach/detach. Inspect or manage company setup
in this chat using the existing Google tools and administrator checks. An absent
personal Google account does not mean company Google access is unavailable.

For a tick, use manage_my_vaults operation=project_select with workspace_path,
kind, item_id and selected from the snapshot. It uses the same settings and
account services as the panel. For external MCP, settings updates the project
selection, workflow_google_account/workflow_slack_account attach or detach the saved account, and
manage_laptop_access selects or removes its computer. mcp_servers.replace is an
exact selection; NO_SERVERS explicitly turns off automatic Vault app selection
for this project. It never disconnects or deletes an account.

Account setup and sharing requests stay in this visible chat. Use manage_my_vaults list/apps,
create, connect, sign_in, sync, set_secret/remove_secret, member/owner operations
or remove_connection as requested. Reuse saved accounts; a provider sign-in may
require the person to open its link. Personal Google and Slack tools can inspect,
connect, reconnect and disconnect the caller's account. Check the applicable
panel-google-apps or panel-slack-account procedure for those operations.
A shared project may use a lent account without giving access to its owner's
other accounts. Existing grants and the caller's own account still decide access.

Store a secret value supplied in chat using the secret tool; never repeat, log,
commit or send it. Confirm success using its name only. For a computer, use
manage_laptop_access devices/get, browse and set to choose allowed folders and
command access. Normal shell/files remain on the server; computers are for
interactive Builder work only, never Goal steps, schedules or Autopilot.

After changing something, read the saved state and explain the result plainly.
The panel refreshes after the turn finishes. A panel click drafts a request into
the same owned tab; do not open another owner's tab or a hidden assistant chat.
