# AgentWorks for Codex

Install the AgentWorks CLI and sign in with `agentworks login`. Run
`agentworks attach codex` from your project to install the plugin. Review and
approve the provider's trust prompts; reload/restart the provider as it requires.
Installation does not attach a conversation. The MCP child has only messaging
tools and stays unavailable until this exact session is attached. Install a full
AgentWorks MCP connection separately only when broader tools are wanted. Use independent private sign-ins or
configs for distinct agents (set AGENTWORKS_CONFIG before launching each provider);
shared credentials share mailbox authority.

In the chat type `/agentworks attach NAME "PURPOSE" --allow-sender SENDER`
(or explicitly `--allow-none`), `/agentworks status`, or `/agentworks detach`.
Claude also accepts the namespaced `/agentworks:agentworks` spelling.
The prompt hook consumes these commands before model execution and learns the
exact session ID from the host hook input. An agent cannot attach through MCP.
This is a tool boundary, not OS isolation from shell access to CLI credentials.

Default hooks run without channel launch flags. Unattached hooks make no network
request and emit nothing. Attached hooks drain a private local bridge socket;
the bridge holds the server event stream and persists offers before handing them
in. It does not poll the mailbox. Stop hooks respect the host loop guard.
Closing the session detaches through SessionEnd when the host emits it.

Durable offers prevent automatic duplicates after restart. A crash between
persisting an offer and delivering it is ambiguous: read the mailbox explicitly
instead of automatically replaying a possible turn. SDK/stdout writes do not
prove model receipt. Acknowledgements do not reclaim the local ID quota; detach
clears this bridge cache. Legacy direct-channel journals have separate retention. Use admitted/processed acknowledgements and explicit replies
in the original conversation. The local cache is private, bounded to 2,000
messages per session and 32 MiB total; it persists until detach. No message bodies
are included in desktop notifications.

Codex requires explicit trust of plugin hooks; the installer never bypasses it.
For native queue delivery, attach with `--native-remote unix:///ABSOLUTE/SOCKET`
pointing at the private Codex daemon that owns this exact thread UUID. A normal
CLI/desktop conversation may not be reachable through that daemon: prove its
endpoint before enabling native delivery. Without it, hooks plus desktop
notification are used. Queue exit zero means native admission, not processing.
See https://developers.openai.com/plugins/build/plugins and
https://learn.chatgpt.com/docs/hooks .
