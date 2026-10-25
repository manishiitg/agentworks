---
name: panel-vault
description: My Vault beside Crew, Goal and Flow chats. Project apps, tools and secrets come first; personal accounts and computers stay below. Connect, repair and share from this chat.
metadata:
  panel-tools:
    work: [manage_my_vaults, settings, manage_google_account, manage_slack_account, manage_laptop_access, list_secrets, set_workflow_secret, delete_workflow_secret, update_project_global_secret_selection]
    agentworks: [manage_my_vaults, settings, manage_google_account, manage_slack_account, list_secrets, set_workflow_secret, delete_workflow_secret]
  external-tools: [get_settings, update_settings, workflow_google_account, workflow_slack_account, manage_laptop_access]
---

# Vault beside this chat

My Vault is the right panel beside the current Crew, Goal or Flow chat. Project
apps and MCPs come first: inspect their tools, choose what this project uses,
or open Add an app for the catalog. Project secrets and Shared from Vault are
open below them. Builders can add, replace or remove a project secret on screen
or ask in chat; shared secret selection never reveals its value or changes the
owner's vault. Google apps and Gmail, the project's Slack account and its
interactive laptop settings have their own expandable sections. Flows do not
gain interactive laptop execution.

Personal setup is folded under My accounts, computers and vaults. It retains
saved Google and Slack accounts, sign-in/reconnection and access controls,
computers and their connection instructions, Slack bots, secret names, owned
and shared vaults, and administrator-only company setup. An empty owned vault
is still available to manage. The panel also shows what this project uses;
its ticks change project use separately from connecting a saved account.
There is no separate My Vault page or hidden setup chat. Do not direct people
to a removed account-menu item; open this right panel.

To open the panel, use the admitted perform_ui_action with view=mcp,
action=open, target=vault in this same chat. The familiar MCPs, Available MCPs,
Secrets, Google apps, Slack account and Local laptop menu/search entries jump
to sections of this same panel; their targets are connected, available, secrets,
gmail, slack-account and laptop. Opening a section changes presentation only.

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
