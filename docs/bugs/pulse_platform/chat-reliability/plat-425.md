[← Platform issue index](../../pulse_platform_issue_register.md)

# PLAT-425 — After a provider switch (or a dead CLI) the next messages were rejected with 409 delivery_uncertain

| Coordination | Value |
|---|---|
| State | fixed on `main`; deploy to Excellence and RTS pending |
| Severity | P1 (the chat looked broken after switching provider; each retry waited 30 s and failed) |
| Date | 2026-10-04 |
| Owner | chat-reliability |
| Related | PLAT-352 (durable submit acknowledgement), PLAT-422 |

## Problem

On Excellence (Code, session `product-e0b588fd`) a conversation with Muse was switched to Codex. The Muse terminal ended at 06:22:25 (`[STOP] released the retained session's active turn`, then `muse tmux session ... died before run completion`).
The next sends to `POST /api/agent-profiles/code/query` returned 409 at 06:22:25, 06:22:56, 06:22:57 (30 s wait) and 06:23:29 (30 s wait). The submission journal marked "hi" `delivery_uncertain`.
A later message ("ok", 06:23:02) went through as `Resuming across providers saved=muse-cli current=codex-cli` and Codex answered.

## Cause

`deliverQueryAsLiveInputNow` tries the retained CLI first. Its warm-session branch treats an error that proves the target is gone (`liveInputErrorProvesNoTarget`: no session registered, session closed) as "start a new turn".
The cold retained-terminal fallback, taken when a retained terminal record exists but the warm session does not, treated the same error ("no active Muse interactive session registered for owner session ...") as uncertain delivery and
answered `writeSubmissionUncertain` (409), so every send stayed rejected until the stale record cleared.

## Fix

The fallback uses the same proof: `liveInputErrorProvesNoTarget(err)` returns false from the live-input path so `handleQuery` starts a fresh turn on the selected provider (logged as `Retained terminal ... has no live target; starting a new turn`).
An error that does not prove "nothing was sent" stays `delivery_uncertain`, which keeps the durable-ack rule against duplicate sends.
Test `TestRetainedTerminalGoneStartsANewTurnInsteadOfDeliveryUncertain` (a retained Muse terminal record whose process is gone; an uncertain failure still gives 409).

## Left

- Deploy and check on Excellence: switch a long Muse chat to Codex and send; the first message must answer without a 409.
- The retained terminal of the OLD provider is still tried first when the provider changed; a provider change could skip the live-input attempt outright (the runtime-change path already restarts the CLI). Not changed here.
- The Stop that ended the Muse terminal came from the provider switch; the user-visible "died before run completion" error on the interrupted turn is expected for a switch mid-turn.
