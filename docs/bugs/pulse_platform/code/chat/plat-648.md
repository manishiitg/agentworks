[← code / chat](index.md)

# PLAT-648: Code chats (tabs) message each other

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | code |
| Area | chat |
| Summary | A Code chat can list the other chats (tabs) of its Code and send one a message that runs there as a visible turn; the reply comes back as an auto-notification |

## What was wanted

Owner, 2026-10-07: "we have multiple tabs in code.. but right now tabs cannot talk to each.. we should enable like
we have communication across products.. same for across tabs inside a code". Side chats are PLAT-571.

## Built

- Two tools in every Code chat (owner turns, not read-only): `list_project_chats` (main chat + side chats with id,
  name, working now, which is this chat) and `message_project_chat(chat, message, reply=true)`
  (`agent_go/cmd/server/code_chat_messages.go`, registered in `registerAgentProfileTools`).
- Delivery reuses the cross-product path of an owner's MCP `ask_crew` into their own chat (`crew_own_chat_ask.go`):
  `productBotTurnRequest` + `startSessionInternalWithResult`, so the message runs as a turn in the target chat,
  queued behind its current turn (durable turn queue), visible in its tab, attributed "[Message from "Chat 2" ...]"
  with `triggered_by=code_chat` (interactive origin: no CLI relaunch, never steered into a running turn).
- The reply comes back like a `call_function` result: a background execution in the sending chat that resumes it
  with an [AUTO-NOTIFICATION] holding the target's final reply. `reply=false` is a hand-off with no answer.
- Who and where come only from the trusted turn: the turn's user, its session and its verified workspace. The
  sending chat must be a registry entry of that Code; targets are read from the same user's conversation registry
  (`projectChats`: the bare project key and `<project>:chat:<id>` only, never trigger/schedule/Slack chats, never
  another Code). A Code is owner-only, so no other person's chat is reachable.
- Loops: a message chain cannot return to a chat already in it (the reply goes back on its own), is at most 3 chats
  long, and one Code sends at most 20 messages per hour between its chats.
- A new side chat now also saves its tab name ("Chat 2") on the server, so the other chats can address it by name.
- Code's system prompt says the tools exist and when to use them.

## Verification

- `TestCodeChatSiblingsAndLoopGuard`: sibling listing excludes isolated automation chats and other Codes; echo,
  hop limit and rate limit are refused.
- `go build`, `go vet`, `tsc -b`, Work frontend tests pass. Unrelated failures already on main:
  `TestPrivateCodeCallerIsSeparateFromCrewWithSameProjectID`, `TestCodePreparedSystemPrompt`,
  `internal/codeproduct` `TestCodeSkillOptionsStayPrivateAndRefreshWithoutMutatingBuiltins`.
- Not verified live: no server was started (a test server's startup updates the machine's global coding CLIs).
  Delivery, queueing and the reply notification were checked by reading the code they reuse.

## Left

- Live check: open a side chat, ask the main chat to message it, see the turn in the side tab and the reply back in
  the main chat; message a busy chat and see it queue.
- A closed side chat stays in the registry and is still listed and reachable; a message reopens its conversation
  (visible from history). If the owner wants closed tabs excluded, closing must mark the slot on the server.
- Side chats opened before this change are listed as "Chat <id>" until renamed.
