[← platform / chat-reliability](index.md)

# PLAT-425 — Switching provider mid-chat killed the running turn and the next messages were rejected with 409 delivery_uncertain

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | chat |
| Area | reliability |
| Summary | fixed on `main`; deploy pending. |

| Coordination | Value |
|---|---|
| State | fixed on `main` (the fallback part `7699e18ab`, the provider-change part in the next commit); deploy to Excellence and RTS pending |
| Severity | P1 (the chat looked broken after switching provider; each retry waited 30 s and failed) |
| Date | 2026-10-04 |
| Owner | chat-reliability |
| Related | PLAT-352 (durable submit acknowledgement), PLAT-422 |

## Problem

On Excellence (Code, session `product-e0b588fd`) the owner sent a message to Muse and, while Muse was still answering, changed the provider on the Models page and sent another message.
The model change was saved (three `PUT .../workflow.json` at 06:22:13-15). The next send, `POST /api/agent-profiles/code/query` at 06:22:24, was followed at 06:22:25 by `[STOP] released the retained session's active turn`
(the running Muse turn ended with `muse tmux session ... died before run completion`) and answered 409. Two more sends got 409 at 06:22:56 and 06:22:57 (30 s wait) and one at 06:23:29. The submission journal marked "hi" `delivery_uncertain`.
A message at 06:23:02 went through as `Resuming across providers saved=muse-cli current=codex-cli` and Codex answered. The 409s lasted until the watchdog removed the stale Muse terminal record (06:22:37, "tmux ... is missing (1/2)").
This is not Muse specific: the delivery, queue and fallback code is shared by every retained coding CLI.

## Cause

1. The decision whether a chat's retained CLI may take the next message (`agentProfileAllowsRetainedLiveInput`) looks only at the product/profile definition. A provider change on the Models page does not alter it, so the retained CLI of the OLD provider stayed the delivery target.
   The existing "a runtime change waits for the running turn" rule (`queueOccupiedConversationTurn`, then `interruptWorkflowPolicySession`) therefore never saw the change for this request; the turn was interrupted by the retained-CLI path instead of being waited for.
2. With the old provider's terminal already gone, the cold retained-terminal fallback answered "no active Muse interactive session registered for owner session" as uncertain delivery (409), although the same error in the warm-session branch means "start a fresh turn".

## Fix

- `retainedCLIProviderDiffers` compares the provider the request selects with the provider of the retained terminal or agent (all CLIs: Claude, Codex, Cursor, Muse, Pi, Agy). A difference counts as a runtime change: the message queues behind a running turn,
  otherwise the old CLI is closed and a fresh turn starts on the selected provider (the native conversation resumes across providers). Logged as `[CHAT_HISTORY] Provider changed for session ...`.
- The retained-terminal fallback applies the same proof as the warm branch (`liveInputErrorProvesNoTarget`): a gone terminal starts a fresh turn; an error that does not prove "nothing was sent" stays `delivery_uncertain` (no duplicate sends).
Tests: `TestProviderSwitchDuringARunningTurnQueuesTheMessage` (through `handleQuery`: a live Muse terminal, a turn in progress, a request for Codex; fails without the fix because the running turn is cancelled),
`TestRetainedCLIProviderDiffersFromTheRequestedProvider`, `TestRetainedTerminalGoneStartsANewTurnInsteadOfDeliveryUncertain`.

## Part 3: an old uncertain submission was never reconciled once the chat ran on another CLI

The first message of the incident ("hi", submission `dfa6ed5b`, 06:22:56) stayed `delivery_uncertain`. The browser re-sent it (same Idempotency-Key) and after its 3 automatic retries (2, 5 and 10 s waits, 30 s each on the server) showed
"Request failed with status code 409 ... Reconcile this submission before sending it again" at 06:40-06:42, while the owner's new Codex messages were confirmed (200).
`canRetryUncertainChatSubmission` proves "not delivered" from the native transcript only when the chat has no live terminal, no running turn and no retained session; the chat had a live Codex terminal, so the old record stayed uncertain for good.

Fix (any CLI, no schema change): a live main terminal that started after the submission was recorded (`snapshot.CreatedAt` after the record's time) cannot hold it, so it and its turns no longer block the proof; a terminal that started before it still keeps it uncertain.
The transcript check stays the proof (the exact prompt present means delivered). Test `TestCanRetryUncertainChatSubmissionIgnoresALiveTerminalThatStartedLater` (fails without the change).

## Left

- Deploy and check on Excellence: start a long Muse turn, change the provider on the Models page, send a message; it must queue (answer `queued_for_turn`), the Muse turn must finish, then Codex answers.
- The earlier "a model or effort change waits for the running turn" rule (`ad3956735`) had no test; the new request-level test covers the same path for a provider change.
- A provider change made while no turn is running closes the old CLI at the next message; nothing closes it earlier.
- After the deploy the stale `hi` record reconciles on its next retry (the proof sees no `hi` in the Codex transcript) and is re-sent as a normal message; the two stale `accepted` records ('ok', 'do you know what are functions we have') are not touched.
- The reconciliation compares only the CURRENT runtime's transcript; a submission sent to a provider that was then replaced is judged by absence there. The record does not store the provider it went to (a field for it would let the old provider's own transcript decide).

## Register notes

[PLAT-425](plat-425.md), P1, fixed on `main`; deploy pending. A gone retained terminal now starts a fresh turn instead of 409.
