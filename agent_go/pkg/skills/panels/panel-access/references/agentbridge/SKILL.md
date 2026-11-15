# AgentBridge beside the project chat

The project menu's AgentBridge entry shows named project agents and reachable
computer/network agents. Point the person to that entry when they want it open;
agent UI controls only open or refresh the Dashboard. The full directory is a
secondary link. Sharing selected agents belongs in Access, not a second
AgentBridge sharing setting.

## Choose the right agent

Crew agents are persistent chat tabs with their own conversations. Use `sessions`
to list, open, fork or ask tabs in this Crew; read `crew-agents` for details.
AgentBridge reaches external coding chats and agents through authorized network
connections. A CLI's internal subagent is a helper inside its provider session,
not automatically a Crew tab or an AgentBridge identity. Do not substitute one
kind of agent for another.

**Connected** and **Discovery** are different: a connection makes the network
available; turning on Discovery lets other people's agents find and message
this agent through AgentBridge. Neither is website sharing. Same-laptop external agents still use
the existing network route, not a separate local transport. Own-agent access
should not require Discovery, but do not assume a particular destination
supports private delivery: use the backend's reachable directory and returned
access result. Do not turn on Discovery as a workaround without the person's
request, or describe private messaging as working without an actual receipt.
For Mac reach, Discovery and server execution, read `references/crew-on-mac/SKILL.md`.

## Discover, send and reply

Use `messages action=list_agents` to discover only agents the caller may reach.
Use the returned target and, when present, chat_id with `messages action=send`;
do not guess an address from a display name. The backend decides whether an
omitted chat_id addresses the Crew's main agent. Product tabs and the external
agent network are distinct, even when their names match.

If the sender asked you a question or for work, send your answer back in the same inbox when you're done, as one short message; don't reply to plain notices or status. Normal final chat text is not forwarded.

Reply in the original conversation using the exact returned reply_address or
inbox_id. Preserve the whole address, including any `network:` prefix; do not
strip it, rebuild it or replace it with a bare inbox ID. Read explicitly sent
messages with `messages action=read` using that address. Carry through a stable
submission_id when supported so a retry does not create a second message.

A connection status is not a message receipt. Accepted/stored means the send
was accepted or saved, not that the receiver saw it or completed the task.
Report the returned state accurately; claim a received reply or completed work
only when the corresponding reply or result exists. Replies arrive later;
continue other work rather than polling or sending acknowledgement loops.

## Keep the person's chat boundaries

Connect or disconnect only the person's own current native chat when they ask;
do not select or attach someone else's chat. Load `crew-agents` in Crew or the
current network guidance for exact own-chat setup, returning the actual setup
link/instructions from the backend. Connecting does not itself turn on Discovery for a chat's agent.

Keep tool names and wire fields (`public`, `published`, `share`, `unshare`) as
returned. Describe network visibility as Discovery on/off, not Publish. Running
an agent online on a server is separate; its publication wording is unchanged.

Sharing keeps the person's own Ask history and selected-agent boundaries. A
message does not let its sender type in or control another person's tab, or
expand their access. Workflows run as Python scripts; discuss them with the
Crew agent and inspect their Runs.


## In-team agents

Teammates' In-team agents are reached only through AgentBridge at the exact `agent:` address returned by discovery; there is no separate in-app Team messaging route or `team:` target. Current membership and In team are checked on every use. Use MCP `messages action=ask` (in Crew chat, `messages action=send` with `ask=true`) for a question: an offline agent's ask goes to the existing Team agent, or refuses if none is available. Direct `messages action=send` waits for the original agent and is never redirected. Read and reply in the original inbox.

Claude Code or Codex can join from the installed plugin Team skill (Claude: `/agentworks:team <Team Crew ID>`) after this exact session is connected and the Team is added to the running Mac app. Join returns the accepted local copy and selected folders; work there for sync. It starts Only me and Answers only; the person chooses In team and optionally Live. Keep the Mac app running. Team keys stay on the server.

Codex terminal delivery uses approved hooks at turn boundaries, not Claude’s idle live channel. For one explicit bounded wait in the exact attached Codex tool call, use `agentworks bridge message watch --provider codex --after 0`, then retain the returned next_cursor. It waits up to20 seconds once and exits; never create a polling loop, approve hooks, or promise immediate idle wake.
