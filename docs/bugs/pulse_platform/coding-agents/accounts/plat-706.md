[← coding-agents / accounts](index.md)

# PLAT-706: A chat's account never changes by itself after a provider switch

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | coding-agents |
| Area | accounts |
| Summary | After a provider switch in a Crew/Code chat, the second message silently pinned the chat to the shared server account and restarted its CLI; the account is now resolved once, recorded and kept |

## What happened

Found in [PLAT-676](../muse/plat-676.md) (Excellence, 2026-10-07). After a provider switch (Codex → Muse), the first turn recorded no account on the conversation and ran on the person's default (their own signed-in account when they have one, `ownDefaultProviderAccountID` in `resolveAgentProfileForQuery`). On the next message the "existing chat stays on the shared account" rule (ea08805b2) filled the missing account with `global:<provider>`, which `bindRuntimeConfiguration` counted as a runtime change: the CLI restarted and the chat moved onto the shared server account, against the shared token limits (PLAT-683/693). The same happened on the second message of every new chat.

Owner decision 2026-10-07: the platform never changes a chat's account by itself; only the person's choice in Models does.

## Fix

- `prepareProductConversationTurn` (`agent_profile_routes.go`): when neither the request (the project's Models choice) nor the chat names an account for the current provider, it resolves one once with `productChatDefaultAccount` (a person's turn: their own signed-in account, else the server account; bot and email turns and global-scope profiles: the server account) and records it on the chat. A recorded account is inherited and never replaced by the server account; an explicit choice still wins and restarts the CLI.
- Recording the account a chat with none recorded already ran on is not a runtime change (`productConversationAccount.FillsUnrecorded` in `bindRuntimeConfiguration`), so it neither restarts the CLI nor drops the native resume.
- Older chats whose record has no account resolve the same default once (the 2026-09-30 pin to the server account is gone; such chats already got `global:` recorded on their second message).
- Models shows the account in use: the conversation response now carries `provider` and `connection_id`, Work keeps them on the tab, and the Models panel falls back from the project's choice to the recorded account, then the own-account default. Next to it a line says "Your own account · not limited" or "Shared account · limits: Day 32% · Week 70% (resets …)" (only the limits that exist; "no limit" when none). The composer chip uses the recorded account too.
- Cost ledger: the turn's `account_id` came from `queryTurnConnection(req)`, while admission used `finalQueryTurnConnection`. They could disagree in two ways: a follow-up that names no account (admission used the session's last account, the ledger recorded the server account), and a non-phase chat whose folder has a manifest (admission checked the manifest's account, the turn ran on and recorded the request's). The ledger now uses `queryTurnConnectionForSession`, and the manifest rule in `finalQueryTurnConnection` applies to workflow phase chats only, as handleQuery's phase block does. For product chats the account is now always set on the request, so both name it.

Test: `TestProviderSwitchKeepsTheChatsAccount` (own account and no own account: first and second turns after the switch on the same account, recorded, no restart on the second; an explicit choice switches). Vitest: `SharedTokenUsageNotice.test.tsx`, `WorkModelsPanel.test.tsx`.

## Left

- A chat already pinned to `global:<provider>` by the old rule stays on the server account (by the same rule: nothing moves it by itself); the person can pick their own account in Models. Not verified live; check one Code chat after the next deploy (switch provider, send two messages, confirm no `[PRODUCT_CHAT] account change` line and the Models line).
- Bot/email turns in a chat whose recorded account is the person's own run on it (one chat, one account).
