# Integrations beside this chat

Use familiar product menu entries: MCPs, Secrets, Google apps, Slack account,
Slack bot and Local laptop. Each opens its native panel directly in the current
Crew or Goal workspace; Flows retain their existing exclusions. Vault remains
the backend account/secret store, not a destination people need to learn.
There is no separate personal Vault page, tab or menu entry.

Use admitted ui with view=mcp, action=open and target=connected,
available, secrets, gmail, slack-account or laptop. Slack bot uses the existing
slack-bot target for Crew and slack for Goal. Opening changes presentation only.

MCPs includes its tool lists, catalog and account setup in chat. Secrets shows one list of project and shared names with their source,
plus project add/replace/remove controls; shared values stay hidden and project_select owns the ticks.
Add uses the panel header, alongside contextual Ask AI. Items are grouped as Yours, Company and This project only, each named with whose it is. A project uses its owner's apps and secrets; there is no sharing of personal accounts with someone else. Company-wide sharing is an administrator's Company groups. Google/Slack sign-in, reconnection
and permissions are under that provider’s Connect or manage accounts. Company
OAuth setup remains administrator-only on that provider panel. Slack bot keeps
its bot editor, setup/test controls and administrator-only company bot. Local
laptop includes connection instructions and project folder/command settings.
No connection selection, grant or execution rule changes with this navigation.

Start with settings action=get for this project, or vault
action=mine_project (workspace_path defaults to this chat's project). The names-only vault snapshot reports
actual selections, whose each item is (source) and connection status. Never infer a secret
value or credentials from this inventory.

Company Google accounts also appear here using the same project-scoped list as
integrations action=google_connections. Their ticks show the existing default account and cannot
be changed with personal-account attach/detach. Inspect or manage company setup
in this chat using the existing Google tools and administrator checks. An absent
personal Google account does not mean company Google access is unavailable.

For a tick, use vault action=mine_project_select with workspace_path,
kind, item_id and selected from the snapshot. It uses the same settings and
account services as the panel. For external MCP, settings updates the project
selection, integrations attach or detach the saved account, and
terminal selects or removes its computer. mcp_servers.replace is an
exact selection; `use_all_vault_apps` false uses only the Vault apps listed there,
true uses every permitted Vault app. It never disconnects or deletes an account.

Account setup requests stay in this visible chat. Use vault mine_apps, mine_connect,
mine_sign_in, mine_sync, mine_set_secret/mine_remove_secret or mine_remove_connection
as requested; they always act on the person's own Vault. Reuse saved accounts; a provider sign-in may
require the person to open its link. Personal Google and Slack tools can inspect,
connect, reconnect and disconnect the caller's account. Check the applicable
panel-channels procedure for those operations.
A shared project may use a lent account without giving access to its owner's
other accounts. Existing grants and the caller's own account still decide access.

Store a secret value supplied in chat using the secret tool; never repeat, log,
commit or send it. Confirm success using its name only. For a computer, use
terminal devices/get, browse and set to choose allowed folders and
command access. Normal shell/files remain on the server; computers are for
interactive Builder work only, never Goal steps, schedules or AgentWorks.

After changing something, read the saved state and explain the result plainly.
The panel refreshes after the turn finishes. A panel click drafts a request into
the same owned tab; do not open another owner's tab or a hidden assistant chat.

GitHub setup: read vault action=mine_github_status first. Offer the methods in
this order: existing gh login (only when gh_available is true for this local
owner), personal access token on local or hosted, then optional OAuth browser
sign-in. Use mine_github_connect with method=gh, method=pat and token, or
method=oauth. A missing OAuth app never blocks PAT. A PAT's repository
permissions decide what works; do not require broad scopes just to connect.
Every method saves the same personal GITHUB_TOKEN secret and selected login.
Using gh explicitly copies its current token; later gh logout/account changes
do not change that saved connection. Never silently fall back to another
identity. Keep the existing Crew secret selection and owner lending rules.
mine_github_disconnect removes the saved connection: native gh and the PAT
itself stay unchanged; OAuth is revoked when possible. Never repeat tokens.
