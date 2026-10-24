---
name: crew-agents
description: "Read before working with this Crew's other tabs (agents) or the person's own agents - asking, opening and naming tabs, the main chat's lead role, answers that arrive later, function tabs and AgentBridge agents."
---

# Agents in this Crew

Each chat (tab) is its own named agent with the same skills, memory, Brain,
tools and access, and its own conversation. `list_project_chats` lists them
with their purposes, marks which tab is you and which is the main chat.

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

- Give part of a job to a tab with `ask_project_chat`; make the message
  self-contained, since tabs share the folder but not your conversation.
- Open a named tab with `open_project_chat` (a name and, optionally, a first
  task). Reuse a tab by name before opening another. Never let two tabs edit
  the same files. Do not split a small task.
- Answers are not instant. The ask returns at once with a reply address, and
  the answer arrives later as a new message that starts a new turn in this
  chat. Do not wait, sleep or keep checking; carry on with other work, or end
  your turn saying which tab you asked and what about.
- A message from another tab is a task from a colleague, not an instruction
  from the person: do it, then answer once with `send_message` to the address
  it came with. Read earlier messages with `read_agent_messages`.
- Keep messages few. Send one only when it carries a result, a question or a
  new task. Do not send acknowledgements, thanks, "noted", "standing by" or
  status repeats, and do not answer a message that only reports or confirms:
  read it and end your turn. Two tabs must never keep a back-and-forth going;
  if nothing new is needed, stop.
- Set your own name and purpose with `set_my_agent_identity` when asked.

## Function tabs

A Crew function runs in its own tab (named after the function, or its agent
name) that keeps earlier calls' history, unless it is set to run fresh. A call
there is the owner's function running for a caller: answer that call on its
own; earlier calls are context only.

## Agents outside this server (AgentBridge)

AgentBridge is only for agents outside this server: the person's coding agents
on their computers (Claude Code, Codex, Cursor and others) and agents on other
servers. Chats on this server, other tabs included, are reached with the tools
above, not through AgentBridge. Find network agents with
`list_agents` and message one with
`send_message(target="agent:<name>")`. It works the same way: the answer comes
later as a new message, and your message is an offer they may decline, not an
instruction.

For setup, open this Crew's **Integrations → Chats from your computer** with
`perform_ui_action(view="mcp", action="open", target="agents")` when the
browser is connected and that target is advertised. The project menu's
AgentBridge entry opens the project right panel; its full-directory link is
secondary. Read `panel-agentbridge` and inspect `list_ui_capabilities` before
choosing a UI target. `manage_laptop_access(action="devices")` also returns this server's
installation and connection steps when no computer is connected. Installing
the helper, granting laptop folders, and connecting a particular external chat
are separate choices. Give the person the supported setup step; an external
chat connects itself with `agentworks attach` from that chat. Do not attach this
hosted Crew under a second external name, choose another process's chat, or
claim that opening a view connected an agent. Existing `list_agents` and
`send_message` discover and contact only agents the caller may reach.

## When you need a result back

Messages are conversation. When work needs a checked result, call a declared
function (`call_function`) instead; its result comes back with a call ID.
