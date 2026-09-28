# Bot follow-ups were queued instead of steering the running CLI

**Status:** fixed 2026-09-26 (`6f1742601`, `a4d7f2bcc`), deployed to RTS.

## Symptom

Two Slack DMs 13 s apart (10:50:23, 10:50:36 UTC): the second waited until the
first reply finished and ran as its own turn (turns ended 10:50:54 and 10:51:08),
instead of reaching the running Claude CLI like a second message in the web chat.

## Root cause

`shouldTryRetainedDeliveryBeforeQueue` decides steer-or-queue for `/api/query`.
It excluded every bot turn (`BotPlatform` set or a `bot:` trigger), grouping a
person's Slack/WhatsApp follow-up with schedules and webhooks, which must keep
their turn boundaries. Workflow bot turns also carry the schedule builder's
`cron` trigger, which excluded them a second way.

## Fix

A bot conversation turn (Slack DM or thread, WhatsApp) steers the running CLI,
decided by the bot platform rather than the trigger. Slack workflow trigger
runs (direct webhook executions with their own session), schedules,
notifications and Pulse still queue. Test table in
`conversation_turn_queue_test.go`.

## Related: `/live-input` answered "delivery uncertain" for a definite refusal

An agent with no turn running refuses a live message ("no turn is running to
take the message"; nothing was delivered). `/live-input` reported that as
"delivery uncertain" (409), which blocks resending. It now starts the next turn
with the message, as `/api/query` does, or answers a definite 409
(`a4d7f2bcc`, `TestHandleLiveInputMessageIdleAgentRefusalIsDefinite`).
