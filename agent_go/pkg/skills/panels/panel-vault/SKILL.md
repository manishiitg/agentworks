---
name: panel-vault
description: The shared Vault panel beside Crew and Goal Builder chats. Inspect apps, secret names, computers and project-use ticks; connect, repair and share from this same chat.
metadata:
  panel-tools:
    work: [manage_my_vaults, settings, manage_google_account, manage_slack_account, manage_laptop_access, list_secrets, set_workflow_secret, delete_workflow_secret, update_project_global_secret_selection]
    agentworks: [manage_my_vaults, settings, manage_google_account, manage_slack_account, list_secrets, set_workflow_secret, delete_workflow_secret]
  external-tools: [get_settings, update_settings, workflow_google_account, workflow_slack_account, manage_laptop_access]
---

# Vault beside this chat

The Vault panel lists apps with connection health, secret names and computers
online or offline. A tick means this project uses that item. Project vaults are
folded below personal and shared vaults; empty vaults are hidden. My vaults opens
the last real Crew or Goal chat with Vault showing. There is no separate Vault
chat or setup form.

To open the panel, use the admitted perform_ui_action with view=mcp,
action=open, target=vault in this same chat. Opening it changes presentation
only.

Start with settings action=get for this project, or manage_my_vaults
operation=project with its workspace_path. The names-only vault snapshot reports
actual selections, sharing counts and connection status. Never infer a secret
value or credentials from this inventory.

For a tick, use manage_my_vaults operation=project_select with workspace_path,
kind, item_id and selected from the snapshot. It uses the same settings and
account services as the panel. For external MCP, settings updates the project
selection, workflow_google_account/workflow_slack_account attach or detach the saved account, and
manage_laptop_access selects or removes its computer. mcp_servers.replace is an
exact selection; NO_SERVERS explicitly turns off automatic Vault app selection
for this project. It never disconnects or deletes an account.

All other requests stay in this visible chat. Use manage_my_vaults list/apps,
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
