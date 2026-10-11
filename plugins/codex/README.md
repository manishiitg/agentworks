# AgentWorks for Codex

Install the AgentWorks CLI and sign in with `agentworks login`. Run
`agentworks attach codex` from your project to install the plugin. Review and
approve the provider's trust prompts; reload/restart the provider as it requires.
Installation does not attach a conversation. The MCP child has only messaging
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
never grants hook trust. A command reaching the model instead of the hook has
not attached the chat; do not ask the model to execute it as a workaround.

In the chat type `/agentworks attach`. The hook proposes a stable name and purpose;
confirm with `/agentworks attach --confirm`, or choose
`/agentworks attach NAME "PURPOSE"`. All senders in your AgentWorks account
can message the attached session; there is no sender allow field.
Use `/agentworks status` or `/agentworks detach` to manage this exact chat.
Claude also accepts the namespaced `/agentworks:agentworks` spelling.
The prompt hook consumes these commands before model execution and learns the
exact session ID from the host hook input. An agent cannot attach through MCP.
This is a tool boundary, not OS isolation from shell access to CLI credentials.

Default hooks run without channel launch flags. Unattached hooks make no network
request and emit nothing. Attached hooks drain a private local bridge socket;
the bridge holds the server event stream and persists offers before handing them
in. It does not poll the mailbox. Stop hooks respect the host loop guard.
Closing the session marks it offline when the host emits SessionEnd. Its name
and journal remain. Submit `/agentworks resume` in the same session to confirm
reconnecting, or `/agentworks detach` to revoke and remove the attachment.

Durable offers prevent automatic duplicates after restart. A crash between
persisting an offer and delivering it is ambiguous: read the mailbox explicitly
instead of automatically replaying a possible turn. SDK/stdout writes do not
prove model receipt. Bodies are cleared on offer and acknowledgement; processed metadata is reclaimed
under quota pressure, and offered metadata after seven days. `/agentworks cleanup`
reclaims offered metadata while retaining pending bodies and the receive watermark.
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

Terminal native attachment also binds a current provider process. Codex desktop
needs `--native-pid PID` and uses the host's `CODEX_THREAD_ID` for replies. Native
idle wake uses that chat's existing approval/sandbox settings. The desktop IPC is
a same-OS-user trust boundary. Native batches above 96 KiB require explicit inbox
recovery. Session moves reuse the restricted child; expired/revoked children are
replaced once from the person's current login. Cleanup retires message-ID reply
metadata; recover with an inbox read and reply using the original `--inbox`.
