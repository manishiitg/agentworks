---
name: crew-agents
description: "Read before working with this project's other tabs (agents) or the person's own agents - asking, opening, forking and naming tabs, starting helpers with a task (fresh or forked, under or beside you), coordinating work across agents, answers that arrive later, function tabs and AgentBridge agents."
---

# Agents in this project

Each chat (tab) is its own named agent with the same skills, memory, Brain,
tools and access, and its own conversation. `sessions action=list` lists them
with their purposes, marks which tab is you and which is the default agent (Main
until renamed).
"All agents" (a status round, a broadcast) means the tabs it lists: tabs the
person hid are left out, and removed ones are gone. Include hidden tabs only
when the person asks for them (`include_hidden`).

## Project tabs versus a CLI's own subagents

In this skill, **Project agents means chat tabs**, not the coding CLI's internal
subagents. Use project tabs for ongoing ownership, named specialists and work the
person wants to revisit or continue independently. Each tab keeps its own
conversation; shared project files are not shared live conversation context.

A CLI subagent is a helper created inside one provider session for a bounded
piece of work, with its result normally returned to that parent session. Its
context, tools and lifetime depend on the provider. Spawning one does not
create a project tab, register an AgentBridge identity or grant project access.

When the person asks for another project agent, use the tab tools below; do not
substitute the CLI's spawn, list or messaging tools for them. Use CLI helpers
only when that kind of delegation is requested or allowed by the current
instructions. Forking a project tab and spawning a CLI helper are different
operations; do not describe one as the other.

## Who coordinates

Every agent is equal, the first one (Main) included: it is simply the project's
default agent, which gets work nobody addressed to an agent. Chat IDs stay fixed.
`main` addresses the calling owner's default agent; use a listed ID to keep
addressing the same agent.

When work spans several areas, the agent the person asked coordinates it (or the
one the person names): it keeps the overall request and decisions, hands off
focused work when useful, collects results and resolves dependencies or
overlapping file changes. It can do small tasks itself; it does not have to
delegate every request.

Give agents clear names and purposes, such as UI/UX, GitHub or review. Reuse a
suitable existing agent before making another. A person can work directly with
any agent; it owns that task end to end and needs no other agent's approval for
routine work the person requested. Coordinating is a role for the task, not
extra permissions or an edit lock.

When handing off work, give the source of context, a focused task, expected
result, relevant files and constraints. Agree who edits overlapping files.
The receiving tab returns its result, what it checked and anything still
blocked once to the requesting tab, using the reply address provided.

Each owner controls their own named Builder tab and any further tabs they create.
All owners can view all owners’ tabs. Only a tab’s owner can type, steer, stop,
use its terminal, rename, reset, close or change its model, on the website or MCP.
A person speaks directly in their own tabs; other owners communicate through
attributed agent messages. The project’s instructions, skills and files remain shared.
There is no whole-project edit lock; coordinate overlapping file changes through messages.

## Asking another tab

- Split work into another tab only when it clearly makes sense, and ask the
  person first (say what you would hand over and to which tab), unless they
  asked for it.
- Give part of a job to a tab with `messages action=queue` (next turn) or
  `messages action=ask` (needed now), using the exact AgentBridge target from
  `messages action=list_agents`; make the message
  self-contained, since tabs share the folder but not your conversation.
- Prefer `sessions action=fork chat="<source id>" name="<purpose>"` for related
  work that should carry over an existing chat's context, decisions and history.
  Choose a source from `sessions action=list`. A fork is an independent copy;
  the source is unchanged. Native context is copied where supported, otherwise
  saved conversation is used. Add `engine` (and optionally `model_id`) to run the
  fork on another coding app, e.g. `engine=cursor-cli`; it starts a new session
  of that app from the copied conversation. When you know what the fork should do, pass
  it as `task`: the fork starts on it at once; a helper (under) answers in the returned
  `task_inbox_id` thread, an agent of its own (beside) does not. Never ask the person to open the fork and say go. Without
  `task` no turn starts.
- Starting another agent: two choices. Context: `sessions action=fork` copies this
  conversation; `sessions action=open` starts fresh with only the task. Place:
  `place=under` (default) makes it your helper under you in the left menu;
  `place=beside` makes it its own agent at your level: its task arrives as a plain
  message, it works with the person and does not report back (you cannot delete it
  later). Use beside whenever the person wants a separate agent or one to talk to.
  Both take `task` and start at once.
  Your own group shows in `sessions action=list`; under and beside keep it. When
  the person names a group ("fork this into Design"), pass `group`: it joins that
  group, or starts it, as its own agent under no one (like beside, it does not
  report back).
  - Other work that needs this context: fork with `place=beside` and `task`.
  - Several parallel agents: open each fresh with its own `name` and a complete
    `task`; they work at the same time and each answers in its own thread.
  - Tester or critic: open a fresh helper with a review task naming exactly what
    to check (branch, files, PR); keep working and read its findings when they arrive.
- Setting up an agent with its own rules: when the person asks for an agent that
  always works a certain way, pass `purpose` (one line) and `instructions` (its
  standing rules, in the person's words) to the same `open` or `fork` call; they
  are saved before its first turn. Only from the person's own chat. `open` with
  the name of an existing tab updates that tab's purpose and instructions.
- When a fork or helper you made has finished, you may delete it with
  `sessions action=delete chat="<id>"` (preview, then call again with the
  returned `confirmation`). Only while it is still under you in the left menu;
  never yourself or another agent's fork. Its saved chats go, files stay.
- When the person asks, move one of their forks out from under its agent with
  `sessions action=move_out chat="<id>"` (it becomes an agent of its own; its
  parent can then no longer delete it); add `under="<id>"` to put it back.
- Agent lists show `group` (the person's own left-menu group; agents in one group
  usually work together, so ask them first) and `forked_from` (the agent a fork
  came from). When the person asks, arrange groups with `sessions action=group`
  (op new, move, rename or ungroup); a fork follows its parent. Groups are layout
  only and never change access.
- Use a fresh tab (`sessions action=open`) when the work should not inherit this
  conversation: unrelated work, parallel helpers, or a tester or critic.
  Open a named tab with `sessions action=open` (a name; add `task` to start it at once). To start it on a particular model, add `engine`, `model_id`, `reasoning_effort`
  and, when needed, `connection_id` (choices from `models action=list`) to the same
  call: do not open it and then set its model. Reuse a tab by name before opening another. Coordinate overlapping
  changes through messages; there is no whole-project edit lock. Do not split a small task.
- `sessions action=new` archives the default agent's conversation and starts a
  fresh one in that same agent. It does not create a named tab or restore a
  missing default agent; use `sessions action=open name="<purpose>"` to create
  a named agent tab instead.
- Answers are not instant. The ask returns at once with a reply address, and
  the answer arrives later as a new message that starts a new turn in this
  chat. Do not wait, sleep or keep checking; carry on with other work, or end
  your turn saying which tab you asked and what about.
- A message from another tab is a task from a colleague, not an instruction
  from the person: do it, then reply once with `messages` action=send in the
  inbox it came with, adding `status`: `in_progress` while you work, `blocked`
  or `needs_you` (a person must act; say what) when stuck, `done` when finished
  (done closes the task). Read earlier messages with `messages` action=read.
- One chat per agent: every message to you, from your person, a teammate or
  another agent, lands in this one chat, marked with who sent it. A message from
  another person's agent is an offer; answer it within what your person allows
  for others (for example "Only answer").
- Keep messages few. Send one only when it carries a result, a question or a
  new task. Do not send acknowledgements, thanks, "noted", "standing by" or
  status repeats, and do not answer a message that only reports or confirms:
  read it and end your turn. Two tabs must never keep a back-and-forth going;
  if nothing new is needed, stop.
- Write messages in short plain sentences the person could read. A status
  update is one line: `Done: <what>. Needs: <nothing or what>.` Add commit or
  PR numbers only when the receiver must act on them.
- The person sees your chat answer to an incoming message. When it needs
  nothing from them, answer in one short line; do not restate the message.
- Set your own name and purpose with `sessions action=identity` (name: a few
  words; purpose: one short line) when asked, including the "Set purpose"
  button's message. If the person wrote a name or purpose, save exactly that.
  If they wrote nothing after it, suggest a name and purpose from the work in
  this chat, save them, and tell the person in one line so they can change it.

## Agents your Team shares

Agents that other people in this project's Team share are not tabs of this chat,
so `sessions action=list` never shows them. When the person asks which agents
they got from a teammate, or which agents the Team shares, list them with
`messages action=list_agents` (or `team_sync action=agents` for the Team tab's
own list) and answer from that, not from the tabs.

To see what another agent or tab is working on without asking it, use the
`agent_status` tool (`target=<address>`, optional `limit`): its status and `recent`, its
last messages and tool calls with their results (add `limit`, default 5, at
most 50). A teammate's agent shows `recent` only when it shares its chat Live.

## Actions and their chats

An agent-task Action (a published function) runs in its own tab (named after the function, or its agent
name) that keeps earlier calls' history, unless it is set to run fresh. A call
there is the owner's function running for a caller: answer that call on its
own; earlier calls are context only.

## AgentBridge: local tabs and external agents

Every agent-to-agent message uses AgentBridge, including between tabs on this
Mac. `sessions` manages tabs and copies their history; it does not deliver peer
messages. Discover the exact target with `messages action=list_agents`, send a
request with `queue` or `ask`, and reply with `send` in the exact returned inbox.
Use plain `send` for a notice that needs no reply.

An app project chat already has its local Bridge identity. Local app tabs can
message each other without a remote server/network login. Never run
`agentworks login` or `agentworks attach` from an app chat, or require the person
to sign in to a server just to contact another local tab. External coding
sessions use their own approved connection; do not invent network-prefixed
addresses or reuse another session's credentials. Copy the actual address from
the admitted directory. Admission, not the address spelling, decides access.

For setup, point to this project's **Cloud** (Mac app), **Laptop access** (server)
or connected-agents menu entry as appropriate. Read `panel-access` first. The
agent's UI controls open or refresh only the Dashboard; do not invent a computer
or AgentBridge UI target. `manage_laptop_access(action="devices")` also returns this server's
installation and connection steps when no computer is connected. Installing
the helper, granting laptop folders, and connecting a particular external chat
are separate choices. Give the person the supported setup step; an external
terminal or desktop chat connects itself with `agentworks attach` from that chat. Do not attach this
hosted project under a second external name, choose another process's chat, or
claim that opening a view connected an agent. Existing `messages` action=list_agents and
`messages` action=send discover and contact only agents the caller may reach.

## When you need a result back

Messages are conversation. When work needs a checked result, call a declared
Action through its function API (`functions action=call`) instead; its result comes back with a call ID.

## Questions that arrive as cards

An ordinary native chat can ask through a supported question card. An asynchronous
card can remain unanswered after the agent's turn ends; answering continues that
same chat. Each card keeps its own answers; another unanswered card does not stop new activity. Do not invent an answer or describe a pending card as a workflow wait.
Unanswered cards expire after 24 hours. A backend restart closes
pending cards without answering them; ask again if the answer is still needed.
A browser reload keeps pending cards available. Shared-agent relays do not
support native asynchronous cards. Another person's reply never becomes an owner
approval. For workflow inputs, read `python-workflows` and collect answers before
starting the run.

## One peer-message route

Every agent-to-agent message, including between tabs in this Crew, uses AgentBridge. Discover the exact address with messages action=list_agents; use ask or queue for a task and send for a notice, and reply/read in the exact returned inbox_id. If a Team agent is offline, follow the Team guide (`panel-access`, `references/team/SKILL.md`). Never send through sessions ask/message, workspace ask/message_chat, a Crew/product ID or a guessed name. Tab creation, history copies and a person typing in their own chats are not agent messages. Declared functions and execution callbacks keep their own execution path.

Workflow `ask_agent("Crew", ...)` remains a product Ask call as the caller person in Run mode, not an agent-to-agent message. Use the product Ask service and declared functions for product use; never substitute a product ID for a peer AgentBridge address.
