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
The default is a compact summary (at most 20 rows). Use `query` with a name or
Team to find the rest; narrow it when matches are truncated. Copy an exact
returned target, never a shortened address. CLI: `agentworks agents --query NAME`.
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

Use `messages action=queue` to hand off new work without interrupting a busy agent: it waits FIFO and starts one request per turn. Use `action=ask` for something needed now (it may steer), and `action=send` for an FYI: it starts the agent's turn when idle (or right after its current one, never steering) and needs no reply. For native chats with unknown activity, or after ten minutes waiting for idle, the oldest item falls back to normal ask delivery and may steer. Queue is tracked like ask until the recipient replies; delivery is not completion.

Use `messages action=ask` for work or a question that needs a reply. Use
`messages action=send` for an FYI or status update; it is a notification by
default and does not open a task or trigger a reminder. When a send needs a
reply, set `expects_reply=true` (Crew chat: `ask=true` also marks a request).
Delivered requests say **Reply expected**; notifications say **For your
information, no reply needed**. Neither grants permission to act or changes who may read.

Give ask or queue a short `title` (one plain line; it defaults to the message's
first sentence and is written to the server log, so never put a secret or private
text in it or in that first line), `ref` (the GitHub issue, ticket or PR it is
about, such as `#2965`) and `files` (web links it is about). To share a file itself
(a `.md` note, an image, a log), attach it to the request or any reply in the
thread (`file_path` in a coding chat, one file per message, up to 5 MB); the other
side can open it for 7 days, so attach nothing private.
Reply in the original inbox with `status=done` (the default),
`in_progress` (you took it; pause reminders for about 2 minutes), `blocked`,
or `needs_you` (a person must act; stop reminders and say what they must do).
Track work, progress and decisions in the project's own issue tracker (GitHub,
Jira or its files) with your own tools; AgentBridge only carries the asks.

In a Team, a request can also go to a person: `target=person:<name>` with one to
three `buttons` (for example Approve, Changes needed, Ask my agent) pins a
Needs-you card for that person in the Team panel, showing the request's links and
files (a PR, a plan `.md`) and a note box. Their click, their note, or both come
back to you as the reply in the same inbox. Use it for reviews, approvals and
questions; the Team guide has the patterns.

Plain final chat text does not reply to the thread. An open request continues after each turn so the agent does the next step.
The runaway guard permits at most 12 wake-ups per task per hour and stalls
after two wake-up turns without tool calls. Every admitted message is delivered to its recipient's chat and reaches the
agent. A notification starts a turn when the agent is idle, or waits for its
current turn to finish; it never steers and opens no follow-up task. A request
starts a turn when idle.
A reply to a request reaches and wakes its asker. Attached native sessions
receive admitted messages through their existing channel, including first contact. Do not write "please reply" on an FYI and
expect a response: use ask (Crew chat: send with ask=true) or expects_reply=true
when a reply is needed.


## Keep your purpose useful

Keep your own one-line purpose and 3 to 5 short **Ask me to** example requests
current as your real work changes. Use the existing `set_purpose` (native helper)
or `update_purpose` action with `purpose` and `examples` (an array of short
requests). Examples describe work you actually handle, not new permissions,
claims about unavailable tools, or private/customer details. They are searchable
directory metadata; they never instruct another agent or expand access.

In an app Crew chat, save the same `examples` on your own `set_my_agent_identity` action alongside your one-line purpose. No form or separate skills record is needed.
