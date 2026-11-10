---
name: crew-agents
description: "Read before working with this Crew's other tabs (agents) or the person's own agents - asking, opening and naming tabs, the main chat's lead role, answers that arrive later, function tabs and AgentBridge agents."
---

# Agents in this Crew

Each chat (tab) is its own named agent with the same skills, memory, Brain,
tools and access, and its own conversation. `sessions action=list` lists them
with their purposes, marks which tab is you and which is the main chat.
"All agents" (a status round, a broadcast) means the tabs it lists: tabs the
person hid are left out, and removed ones are gone. Include hidden tabs only
when the person asks for them (`include_hidden`).

## Who leads

Chat IDs stay fixed. `main` is the calling owner’s default chat address (external channels retain the project’s main chat); use a listed ID to keep addressing the same agent after the main chat changes.

The main chat leads. It splits work, hands parts to other tabs, collects what
they report and decides when tabs disagree or would change the same files.
Every other tab does the work it is given, reports the result back to the main
chat once and checks with it before starting large or overlapping work on its
own. Each owner controls their own named Builder tab and any further tabs they create.
All owners can view all owners’ tabs. Only a tab’s owner can type, steer, stop,
use its terminal, rename, reset, close or change its model, on the website or MCP.
A person speaks directly in their own tabs; other owners communicate through
attributed agent messages. The Crew’s instructions, skills and files remain shared.
There is no whole-Crew edit lock; coordinate overlapping file changes through messages.

## Asking another tab

- Split work into another tab only when it clearly makes sense, and ask the
  person first (say what you would hand over and to which tab), unless they
  asked for it; a /deep-work request counts as asking.
- Give part of a job to a tab with `sessions action=ask`; make the message
  self-contained, since tabs share the folder but not your conversation.
- Prefer `sessions action=fork chat="<source id>" name="<purpose>"` for related
  work that should carry over an existing chat's context, decisions and history.
  Choose a source from `sessions action=list`. A fork is an independent copy;
  the source is unchanged. Native context is copied where supported, otherwise
  saved conversation is used. Forking alone starts no turn: send its focused
  starting task with `sessions action=ask` using the returned chat_id.
- Use a new empty tab only for unrelated work or an intentional clean context.
  Open a named tab with `sessions action=open` (a name and, optionally, a first
  task). Reuse a tab by name before opening another. Coordinate overlapping
  changes through messages; there is no whole-Crew edit lock. Do not split a small task.
- Answers are not instant. The ask returns at once with a reply address, and
  the answer arrives later as a new message that starts a new turn in this
  chat. Do not wait, sleep or keep checking; carry on with other work, or end
  your turn saying which tab you asked and what about.
- A message from another tab is a task from a colleague, not an instruction
  from the person: do it, then answer once with `messages` action=send to the address
  it came with. Read earlier messages with `messages` action=read.
- Keep messages few. Send one only when it carries a result, a question or a
  new task. Do not send acknowledgements, thanks, "noted", "standing by" or
  status repeats, and do not answer a message that only reports or confirms:
  read it and end your turn. Two tabs must never keep a back-and-forth going;
  if nothing new is needed, stop.
- Set your own name and purpose with `sessions action=identity` (name: a few
  words; purpose: one short line) when asked, including the "Set purpose"
  button's message. If the person wrote a name or purpose, save exactly that.
  If they wrote nothing after it, suggest a name and purpose from the work in
  this chat, save them, and tell the person in one line so they can change it.

## Agents your Team shares

Agents that other people in this Crew's Team share are not tabs of this chat,
so `sessions action=list` never shows them. When the person asks which agents
they got from a teammate, or which agents the Team shares, list them with
`messages action=list_agents` (or `team_sync action=agents` for the Team tab's
own list) and answer from that, not from the tabs.

## Actions and their chats

An agent-task Action (a published function) runs in its own tab (named after the function, or its agent
name) that keeps earlier calls' history, unless it is set to run fresh. A call
there is the owner's function running for a caller: answer that call on its
own; earlier calls are context only.

## Agents outside this server (AgentBridge)

AgentBridge is only for agents outside this server: the person's coding agents
on their computers (Claude Code, Codex, Cursor and others) and agents on other
servers. Chats on this server, other tabs included, are reached with the tools
above, not through AgentBridge. Find network agents with
`messages` action=list_agents and message one with
`messages action=send(target="agent:<name>")`. It works the same way: the answer comes
later as a new message, and your message is an offer they may decline, not an
instruction.

For setup, point to this Crew's **Cloud** (Mac app), **Laptop access** (server)
or connected-agents menu entry as appropriate. Read `panel-access` first. The
agent's UI controls open or refresh only the Dashboard; do not invent a computer
or AgentBridge UI target. `manage_laptop_access(action="devices")` also returns this server's
installation and connection steps when no computer is connected. Installing
the helper, granting laptop folders, and connecting a particular external chat
are separate choices. Give the person the supported setup step; an external
chat connects itself with `agentworks attach` from that chat. Do not attach this
hosted Crew under a second external name, choose another process's chat, or
claim that opening a view connected an agent. Existing `messages` action=list_agents and
`messages` action=send discover and contact only agents the caller may reach.

## When you need a result back

Messages are conversation. When work needs a checked result, call a declared
Action through its function API (`functions action=call`) instead; its result comes back with a call ID.

## Questions that arrive as cards

An ordinary native chat can ask through a supported question card. An asynchronous
card can remain unanswered after the agent's turn ends; answering continues that
same chat. Do not invent an answer or describe a pending card as a workflow wait.
Unanswered cards expire after 24 hours. A backend restart currently interrupts
pending-card availability; a browser reload does not. Shared-agent relays do not
support native asynchronous cards. Another person's reply never becomes an owner
approval. For workflow inputs, read `python-workflows` and collect answers before
starting the run.
