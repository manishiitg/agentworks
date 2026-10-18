# AgentWorks tools for Claude Code (no hooks)

Skills and the AgentWorks message tools only. No hooks run, so nothing shows
as "blocked" in the chat. Install with `agentworks setup claude --no-hooks`.

Say "connect this chat to AgentWorks", "publish yourself to the network", "which agents
can you see?" or "ping them". The chat is identified by Claude Code itself on
every call.

What this gives up compared with the full plugin (`agentworks setup claude`):
no turn-end delivery fallback if the live channel drops, no automatic
reconnect after a restart (ask once: "connect this chat"), and no typed
`/agentworks` shortcuts. Joining another network (`--also`) is the person's own
request, run by the agent as `agentworks attach --also LOGIN` in the chat; it is
never a tool call.

Use one plugin or the other: remove this one before installing `agentworks`
(`claude plugin uninstall agentworks-tools@agentworks --scope user`).
