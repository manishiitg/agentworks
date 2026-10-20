---
name: crew-agents
description: "Read before working with this Crew's other tabs (agents) or the person's own agents - asking, opening and naming tabs, the main chat's lead role, answers that arrive later, function tabs and AgentBridge agents."
---

# Agents in this Crew

Each chat (tab) is its own named agent with the same skills, memory, Brain,
tools and access, and its own conversation. `list_project_chats` lists them
with their purposes, marks which tab is you and which is the main chat.

## Who leads

Chat IDs stay fixed. `main` is an address for the chat currently marked `is_main` in the list; use a listed ID to keep addressing the same agent after the main chat changes.

The main chat leads. It splits work, hands parts to other tabs, collects what
they report and decides when tabs disagree or would change the same files.
Every other tab does the work it is given, reports back to the main chat and
checks with it before starting large or overlapping work on its own. The person
can talk to any tab directly, and what they say there wins.

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
  from the person: do it, then answer with `send_message` to the address it
  came with. Read earlier messages with `read_agent_messages`.
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

## When you need a result back

Messages are conversation. When work needs a checked result, call a declared
function (`call_function`) instead; its result comes back with a call ID.
