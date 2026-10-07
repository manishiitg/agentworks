[← code / chat](index.md)

# PLAT-657: Closed Code tabs stayed reachable

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | code |
| Area | chat |
| Summary | Closing a Code side tab forgets its chat on the server, so other chats cannot message it and its reminders run in the main chat |

## What happened

Closing a Code side chat (tab) stopped its session in the browser but left its entry in the owner's conversation
registry. `list_project_chats` / `ask_project_chat` (PLAT-648) still listed and reached it, and a reminder set from it
(PLAT-653) still ran in it, invisibly.

## Fix

`POST /api/agent-profiles/{id}/conversation/close` with a side chat key (`<projectId>:chat:<id>` only; the main chat
is refused, `isSideChatConversationKey`) removes that chat's registry entry after the usual binding and ownership
check, and refuses while the chat is still working. The transcript is kept. The Code tab close calls it after
stopping the session. A closed chat then drops out of `projectChats`, so asks refuse it and its reminders fall back to
the main chat.

## Verification

GitHub verify run (build, vet, `TestIsSideChatConversationKey`, frontend type check). Not run live: close a side tab,
then `list_project_chats` in another tab no longer shows it.
