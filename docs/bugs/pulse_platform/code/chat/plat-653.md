[← code / chat](index.md)

# PLAT-653: Code reminders follow their tab; check before scheduling

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | code |
| Area | chat |
| Summary | Reminders set from a Code side chat run in that tab; the agent checks first and never deletes a reminder the user asked for |

## What happened

RTS, Code project "sde private", side chat "task2", 2026-10-07 13:00 IST. The owner asked "set a reminder to check
this in 15min". The agent created a one-time schedule for 07:45 UTC, then in the same turn noticed its last check
was an hour old, checked, found the deploy finished at 06:42, and deleted the reminder as "pointless". The owner:
"you just said its pointless".

Two problems:
1. Order and override. It should have checked once first (cheap) and reported, offering a reminder; and a reminder
   the user asked for must not be deleted without asking. Neither the Code prompt nor the `work-schedules-and-bots`
   skill mentioned one-time reminders at all (the skill only described recurring cron schedules).
2. Wrong chat. A project schedule always ran in the project's main chat; nothing recorded which chat (tab) created it,
   so task2's reminder would have fired in the main chat.

## Fix

- `productschedule.Schedule.ChatKey`: the creating side chat (`<projectId>:chat:<id>`). `create_project_schedule`
  records it when a Code side chat creates a non-isolated schedule (the chat comes from the trusted turn via
  `codeChatsFor`, never from tool arguments). When the schedule fires it runs in that chat while it is still one of
  the project's chats, otherwise in the main chat; `conversationKeyForJob` gives it that chat's own queue.
  `list_project_schedules` shows `chat_key`. Only keys of the same project are honoured (`scheduleChatKey`).
- Guidance: the skill has a "Reminders" section (one-time `in_minutes` / `run_at`; check first when cheap and report
  instead of scheduling if settled; never create-then-delete in one turn; a requested reminder stays unless the user
  agrees), and the schedules prompt line says the same in one sentence.

## Verification

GitHub verify run (build, vet of changed packages, `TestProjectScheduleRunsInItsSideChat` and the existing
schedule-tool tests). Not run live: after a deploy, set a reminder from a Code side tab and see it arrive there; close
the tab first and see it arrive in the main chat.
