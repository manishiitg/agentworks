# AgentWorks for Codex

## Codex app

A separate Codex CLI on PATH is not required. Install the AgentWorks CLI and
sign in, then use the app to install the plugin:

1. Open **Settings → Plugins → Browse directory**.
2. Select **Add → Add a marketplace**. Enter `manishiitg/agentworks-plugins`
   in **Source**, then select **Add marketplace**.
3. Choose the `agentworks` source, open **AgentWorks** (`agentworks-codex`),
   and select **Install plugin**.
4. In **Settings → Plugins**, check that AgentWorks is enabled.
5. In **Settings → Hooks**, review the four AgentWorks definitions under
   **From Plugins** and trust those you intend to use. Use **Reload hooks**
   when prompted. Reload the app or start a new chat as required.
6. Type `/agentworks attach` in the exact chat you want to connect.

These labels were inspected in the macOS app's installed UI. If its marketplace
menu is unavailable, after signing in run `agentworks attach` in your own
terminal. Current AgentWorks finds the Codex executable on PATH or in a
supported app bundle, offers a missing-plugin install and lets you select a
verified live chat. It does not approve the app's hooks.

The app and its bundled CLI use the same Codex home by default: plugin settings
in `~/.codex/config.toml`, installed packages in `~/.codex/plugins/cache/`.
A custom `CODEX_HOME` must match in both. Plugin setup/profile matching does
not establish native chat discovery from a custom home; that path still needs
separate acceptance. The existing native proof uses the default home. The Mac app's bundled plugin list
was checked against the existing enabled AgentWorks package loaded by the app;
this does not claim a fresh installation or new trust approval.
[Official marketplace/configuration guidance](https://developers.openai.com/plugins/build/plugins).

To update in the app, open **Settings → Plugins → Marketplace**, select
**Upgrade** for `agentworks`, then reload as requested and check the package version. Preserve your
plugin enablement choice and review changed hooks. `agentworks update` updates
AgentWorks separately and can use the supported app-bundled Codex executable.
Windows/Linux app fallback is used only when a verified layout exists; otherwise
use the in-app flow and report the unsupported discovery result.

The [five-step owner test](https://github.com/manishiitg/agentworks/blob/main/docs/codex-app-live-test.md)
checks a fresh app installation, hook review and exact-chat receipts after deployment.

## Codex CLI

```sh
codex plugin marketplace add manishiitg/agentworks-plugins
codex plugin add agentworks-codex@agentworks
```

Review hooks with `/hooks` and reload the CLI as needed.

Install the AgentWorks CLI and sign in with `agentworks login`. Run
`agentworks attach` from your terminal to install the plugin if missing and select a verified live chat. Review and
approve the provider's trust prompts; reload/restart the provider as it requires.
Installation alone does not attach a conversation; selection or an in-chat person command does. The MCP child has only messaging
tools and stays unavailable until this exact session is attached. Install a full
AgentWorks MCP connection separately only when broader tools are wanted. The daemon derives a private messaging-only child connection for each attached
agent. It is bound to that stable agent ID/name, and parent sign-out revokes it.
Named-peer messages use this child; product functions require a separate connection.

This package uses Codex's supported `.codex-plugin/plugin.json` compatibility
manifest and `.mcp.json` transport file. The installed loader must report two
skills, the `agentworks` MCP server and four hooks: SessionStart, SessionEnd,
UserPromptSubmit and Stop. Some installed Codex versions discover skills from a
portable root manifest but ignore its OpenAI extension and compatibility-shaped
MCP file; a skills-only listing is an incomplete installation.

Installing a plugin and adding its marketplace are separate entries in Codex:
the marketplace supplies updates, while the installed plugin's toggle controls
loading. Enable the plugin, reload Codex and review its hooks. In the Codex CLI,
use `/hooks` to review and trust the exact AgentWorks definitions. Installation
never grants hook trust. The SessionEnd hook uses Codex's three-second
timeout limit so the provider does not report a clamped-timeout loading warning. A command reaching the model instead of the hook has
not attached the chat. With a current AgentWorks build, a person may instead
ask the agent to attach its own automatically detected session; this does not
install or trust a plugin.

In the chat type `/agentworks attach` to connect or reconnect immediately with a short stable name, or choose `/agentworks attach NAME`. All senders in your AgentWorks account
can message the attached session; there is no sender allow field.
Use `/agentworks agents` or `/agentworks detach` to manage this exact chat.
Claude also accepts the namespaced `/agentworks:agentworks` spelling.
The prompt hook consumes these commands before model execution and learns the
exact session ID from the host hook input. An agent cannot attach through MCP.
This is a tool boundary, not OS isolation from shell access to CLI credentials.

Default hooks run without channel launch flags. Unattached hooks make no network
request and emit nothing. Attached hooks drain a private local bridge socket;
the bridge holds the server event stream and persists offers before handing them
in. It does not poll the mailbox. Stop hooks respect the host loop guard.
Closing the session marks it offline when the host emits SessionEnd. Its name
and journal remain. Type `/agentworks attach` in the same session to reconnect, or `/agentworks detach` to revoke and remove the attachment.

Durable offers prevent automatic duplicates after restart. A crash between
persisting an offer and delivering it is ambiguous: read the mailbox explicitly
instead of automatically replaying a possible turn. SDK/stdout writes do not
prove model receipt. Bodies are cleared on offer and acknowledgement; processed metadata is reclaimed
under quota pressure, and offered metadata after seven days. Automatic cleanup reclaims offered metadata while retaining pending bodies and the receive watermark.
Retired messages require an explicit inbox read/reply. Legacy direct channels
store bounded metadata without bodies. Use admitted/processed acknowledgements
and explicit replies in the original conversation. The private local cache is
bounded to 2,000 pending messages per session and 32 MiB total. No message bodies
are included in desktop notifications.

Codex requires explicit trust of plugin hooks; the installer never bypasses it.
For native queue delivery, attach with `--native-remote unix:///ABSOLUTE/SOCKET`
pointing at the private Codex daemon that owns this exact thread UUID. A normal
CLI/desktop conversation may not be reachable through that daemon: prove its
endpoint before enabling native delivery. Without it, hooks plus desktop
notification are used. Queue exit zero means native admission, not processing.
See https://developers.openai.com/plugins/build/plugins and
https://learn.chatgpt.com/docs/hooks .

Existing native sessions can use explicit desktop/session transports and the
process-bound `agentworks bridge message` fallback. See [native transports](../../docs/communication-protocols.md#existing-desktop-and-claude-session-transports).

Terminal attach discovers live Claude registrations and verifies Codex candidates with the exact desktop owner before offering a person selection. Technical process/session overrides are hidden from normal help. Native idle wake uses the chat's existing approval/sandbox settings. The desktop IPC is a same-OS-user trust boundary. Native batches above 96 KiB require explicit inbox recovery. CLI updates and stop/start restore still-live existing attachments without rotating their identity or repeating AgentWorks guidance. A provider process restart requires the person to type attach again. Claude itself adds a notice to every native session message; AgentWorks cannot suppress that host notice.

The person chooses only the attachment name. The attached agent can describe its work with `the agentworks tool (action=set_purpose)` or `agentworks bridge message purpose --provider codex --purpose "WHAT YOU WORK ON"`. On macOS, optional `terminal-notifier` provides a notification click action for the exact Codex thread; without it, the basic alert must be followed by opening the chat manually.

### Updates from GitHub

The marketplace source is `manishiitg/agentworks-plugins` on GitHub, a small repository generated from this one (installing from the main repository still works; to switch, run `codex plugin marketplace remove agentworks` and `codex plugin marketplace add manishiitg/agentworks-plugins`, then `codex plugin add agentworks-codex@agentworks`). Refresh it and
install the newest published plugin; reload the provider as required.

```sh
codex plugin marketplace upgrade agentworks
codex plugin add agentworks-codex@agentworks
```

Plugin updates do not update the AgentWorks CLI or server, attach chats, or approve hook trust.

On a direct person request, the agent may run `agentworks attach [NAME]` for its own detected chat without installing a plugin. It may also run own detach; Codex limits this to leases originally created through own CLI attach. Person-created Codex leases retain the person detach command. No session overrides, other-chat selection or trust changes are permitted. `agentworks agent` aliases `agents`; purpose may describe responsibilities in up to 512 characters. Routing hints do not isolate same-user credentials.
