[← code / chat](index.md)

# PLAT-648: Code chats (tabs) message each other

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | code |
| Area | chat |
| Summary | A Code chat can list the other chats (tabs) of its Code and ask one; the ask is a function call (call_id, saved record, structured result) that runs there as a visible turn, and the result comes back as the standard call auto-notification |

## What was wanted

Owner, 2026-10-07: "we have multiple tabs in code.. but right now tabs cannot talk to each.. we should enable like
we have communication across products.. same for across tabs inside a code". Side chats are PLAT-571.

## Built (first version, replaced below)

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

## Rebuilt on the function-call mechanism (2026-10-07)

Owner: messaging between chats must be a proper call, like the cross-product calls. The first version (above) had no
call record: the reply was the target's last text, held in memory, with its own loop guard and rate limit.

- **Target kind.** A sibling chat is a `triggerTarget` of the caller's own Code with `Chat` set (and the caller carries
  its own `Chat`), both built only from the trusted turn by `codeChatsFor`. `startCrewFunctionCall` records it like
  any call and runs it with `runCodeChatCall` (`code_chat_messages.go`): a turn in that chat's existing conversation
  (`productBotTurnRequest` + `startSessionInternalWithResult`, `triggered_by=code_chat`, label "From <chat>"), queued
  behind its current turn and visible in its tab. No trigger binding is created.
- **Tool surface.** `list_project_chats` stays. `message_project_chat` is replaced by
  `ask_project_chat(chat, message, reply=true, submission_id, timeout_minutes)`, shaped like the other asks
  (`ask_crew`, `call_function` with `ask`): it returns the call snapshot (`call_id`, `status`) at once, and the result
  arrives as the standard function-call [AUTO-NOTIFICATION] (`startCrewFunctionWatch`). `get_function_call(call_id)`
  gives status, progress, pending inputs and the target chat's recent activity; `reply_function_call` answers a
  question the target chat asks. `ask_function_update` is refused for chats (they take no mid-turn messages).
- **reply=false** is `notify=false` of `call_function`: the call is still created and recorded, the target is told it
  is a hand-off, and no notification is started; the sender can still read it with `get_function_call`.
- **Callee contract.** The target chat gets the standard `[Function call fn-...]` task with
  `report_function_progress` / `return_function_result(call_id, result={"answer": ...})`. The result is what it
  returns there; only if its turn ends without one is its final reply used as the answer (as for any free-text ask),
  so a human-visible chat never gets an extra "result missing" retry turn.
- **Call features.** `fn-<uuid>` call_id, saved record + call index (`chat_history/code-peer-calls`, survives a
  restart as "interrupted"), lookup by call_id, progress, structured result or error, timeout from the target chat's
  last activity (default 60 min, late answers still delivered, hard cap 4x), `submission_id` idempotency (scoped to
  the sending chat) and joining of identical in-flight asks (same sender chat, target chat and arguments).
- **Only those two chats.** For a chat call, `get_function_call` / `reply_function_call` accept only the sending
  chat's session and `report_function_progress` / `return_function_result` only the target chat's session; the
  Code-peer authorization is replaced by "same Code, same folder" for these calls.
- **Loops: the shared chain guards.** Chain keys are scoped per chat (`<code key>#chat:<conversation key>`), so the
  chats of one Code are separate participants: no revisits (a chat, or a chat's Code, already in the chain),
  `crewFunctionMaxDepth` (4) and `crewFunctionChainBudget` (20 calls per chain). A Code's plain `call_function` joins
  the chain of a call into any of its chats. Removed: PLAT-648's `codeChatRelay` chain tracking, the 3-chat hop limit
  and the 20-messages-per-hour rate limit.
- Security rules unchanged: sender and project only from the trusted turn; targets only the same user's main chat and
  side chats of the same Code (bare project key or `<project>:chat:<id>`), never trigger, schedule or Slack chats,
  other Codes or other users; a non-chat caller cannot target a chat. No tokens are involved.

Not covered by the shared guards (as for Crews): a chat that, in each turn resumed by an answer, asks again starts a
fresh chain each time. The removed per-Code hourly limit used to bound that; the owner chose the shared guards.

Verification: `TestCodeChatAskIsAFunctionCall` (replaces `TestCodeChatSiblingsAndLoopGuard`): sibling listing,
refusal of self, of a chat outside the Code and of a non-chat caller; an ask gets an `fn-` call_id and a saved index,
the callee sees the call instructions, the sender cannot answer its own call, an ask back to the sender is refused by
the shared chain guard, progress and `return_function_result` settle it, the sender reads it with
`get_function_call`, and the same `submission_id` returns the same call. Related server tests pass; the failures in
the package also fail on origin/main. Not verified live (no isolated server was started: its startup updates the
machine's global coding CLIs); delivery into the chat, queueing and the notification were checked by reading the code
they reuse.

## Verification (first version)

- `TestCodeChatSiblingsAndLoopGuard`: sibling listing excludes isolated automation chats and other Codes; echo,
  hop limit and rate limit are refused.
- `go build`, `go vet`, `tsc -b`, Work frontend tests pass. Unrelated failures already on main:
  `TestPrivateCodeCallerIsSeparateFromCrewWithSameProjectID`, `TestCodePreparedSystemPrompt`,
  `internal/codeproduct` `TestCodeSkillOptionsStayPrivateAndRefreshWithoutMutatingBuiltins`.
- Not verified live: no server was started (a test server's startup updates the machine's global coding CLIs).
  Delivery, queueing and the reply notification were checked by reading the code they reuse.

## Left

- Live check: open a side chat, have the main chat `ask_project_chat` it, see the `[Function call ...]` turn in the
  side tab and the result back in the main chat; ask a busy chat and see it queue; `reply=false` hand-off.
- A closed side chat stays in the registry and is still listed and reachable; a message reopens its conversation
  (visible from history). If the owner wants closed tabs excluded, closing must mark the slot on the server.
- Side chats opened before this change are listed as "Chat <id>" until renamed.
