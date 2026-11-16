# AgentBridge beside the project chat

The project menu's AgentBridge entry shows named project agents and reachable
computer/network agents. Point the person to that entry when they want it open;
agent UI controls only open or refresh the Dashboard. The full directory is a
secondary link. Sharing selected agents belongs in Access, not a second
AgentBridge sharing setting.

## Choose the right agent

Crew agents are persistent chat tabs with their own conversations. Use `sessions`
to list, open or fork tabs in this Crew; read `crew-agents` for details.
AgentBridge messages app tabs and external coding agents through authorized network
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
Use the exact returned `agent:` or `network:` target with `messages action=send`;
do not guess an address from a display name. App tabs and terminal agents use
the same Bridge identity and reply thread. A product ID is for its Ask entry
point or declared functions, never a peer-chat destination.

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


## shared agents

Teammates' shared agents are reached only through AgentBridge at the exact `agent:` address returned by discovery; there is no separate in-app Team messaging route or `team:` target. Current membership and Shared with team are checked on every use. Use MCP `messages action=ask` (in Crew chat, `messages action=send` with `ask=true`) for a question: an offline agent's ask goes to the existing Team agent, or refuses if none is available. Direct `messages action=send` waits for the original agent and is never redirected. Read and reply in the original inbox.

Claude Code or Codex joins a Crew from the installed plugin Crew skill (Claude: `/agentworks:crew <Crew ID>`), or its `agentworks` tool `action=join` with `project=<Crew ID>`; explicit `action=leave` removes it. This exact session must already be connected. The person must own the Crew or be a current member of its active Team, not just have dashboard Run access. The server returns Crew instructions and an owner-admitted local folder or accepted Team copy; follow the instructions and work there. With no local copy it returns `copy=null`, `folders=[]`: the files stay on the server; ask its agents or add the Crew to this Mac in AgentWorks. Never supply or guess a server/local path or a native session. Keep the Mac app running for shared sync. A new join follows the Crew's Team: Shared with team, Answers only, no change permission when on; only the person and their own agents when off. Turning Team on later needs no rejoin; rejoin keeps Only me/Live choices. The owner can read their own exact terminal journal; other people's native text requires explicit Live. One terminal agent joins one Crew until it leaves. Saved Discovery stays saved but grants no audience while joined; leaving restores it. Team keys stay server-only.

Codex terminal delivery uses approved hooks at turn boundaries, not Claude’s idle live channel. For one explicit bounded wait in the exact attached Codex tool call, use `agentworks bridge message watch --provider codex --after 0`, then retain the returned next_cursor. It waits up to 20 seconds once and exits; never create a polling loop, approve hooks, or promise immediate idle wake.


## Requests and notifications

Use `messages action=ask` for work or a question that needs a reply. Use
`messages action=send` for an FYI or status update; it is a notification by
default and does not open a task or trigger a reminder. When a send needs a
reply, set `expects_reply=true` (Crew chat: `ask=true` also marks a request).
Delivered requests say **Reply expected**; notifications say **For your
information**. Neither grants permission to act or changes who may read.

Reply in the original inbox with `status=done` (the default), `in_progress`
(work remains; pause reminders for about 2 minutes), `blocked`, or
`needs_owner` (stop reminders and surface the reason to the person). Plain
final chat text does not reply to the thread. An open request continues after each turn so the agent does the next step.
The runaway guard permits at most 12 wake-ups per task per hour and stalls
after two wake-up turns without tool calls. Notifications do not start an app-chat turn or open a follow-up task. An
attached native session still receives admitted notifications through its live
channel, including first contact. Do not write "please reply" on an FYI and
expect a response: use ask (Crew chat: send with ask=true) or expects_reply=true
when a reply is needed.
