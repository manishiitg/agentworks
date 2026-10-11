# AgentWorks for Claude

Manual plugin install: `/plugin install agentworks --marketplace manishiitg/agentworks`.

Install the AgentWorks CLI and sign in with `agentworks login`. Run
`agentworks attach claude` from your project to install the plugin. Review and
approve the provider's trust prompts; reload/restart the provider as it requires.
Installation does not attach a conversation. The MCP child has only messaging
tools and stays unavailable until this exact session is attached. Install a full
AgentWorks MCP connection separately only when broader tools are wanted. The daemon derives a private messaging-only child connection for each attached
agent. It is bound to that stable agent ID/name, and parent sign-out revokes it.
Named-peer messages use this child; product functions require a separate connection.

In the chat type `/agentworks attach`. The hook proposes a stable name;
confirm with `/agentworks attach --confirm`, or choose
`/agentworks attach NAME`. All senders in your AgentWorks account
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

For optional idle native wake, launch with `agentworks claude`, approve Claude's
development-channel confirmation, then attach inside the chat. During preview,
custom marketplace installation alone does not approve our channel. An admin can
approve it through allowedChannelPlugins; Team/Enterprise needs channelsEnabled.
Without a channel launch the hooks remain the default route. The plugin channel
learns the exact session ID through the person hook and binds only to its own
Claude ancestor process (PID plus start time), never another chat in the project.
See https://code.claude.com/docs/en/channels .

Existing native sessions can use explicit desktop/session transports and the
process-bound `agentworks bridge message` fallback. See [native transports](../../docs/communication-protocols.md#existing-desktop-and-claude-session-transports).

Terminal native attachment also binds a current provider process. Codex desktop
needs `--native-pid PID` and uses the host's `CODEX_THREAD_ID` for replies. Native
idle wake uses that chat's existing approval/sandbox settings. The desktop IPC is
a same-OS-user trust boundary. Native batches above 96 KiB require explicit inbox
recovery. Session moves reuse the restricted child; expired/revoked children are
replaced once from the person's current login. Cleanup retires message-ID reply
metadata; recover with an inbox read and reply using the original `--inbox`.

The person chooses only the attachment name. The attached agent can describe its work with `agent_message_set_purpose` or `agentworks bridge message purpose --provider claude --purpose "WHAT YOU WORK ON"`. Purpose updates cannot attach, rename or grant permissions.

### Updates from GitHub

The marketplace source is `manishiitg/agentworks` on GitHub. Refresh it and
install the newest published plugin; reload the provider as required.

```sh
claude plugin marketplace update agentworks
claude plugin update agentworks@agentworks
```

Plugin updates do not update the AgentWorks CLI or server, attach chats, or approve hook trust.
