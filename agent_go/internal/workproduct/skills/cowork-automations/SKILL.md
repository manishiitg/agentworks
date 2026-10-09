---
name: cowork-automations
description: "Turn a repeating task into an automation for a non-technical person in a Cowork space: a schedule or a webhook that sends the work to this chat, explained in plain language, tried once first, with a clear way to pause it"
---

# Cowork: make it happen on its own

When someone describes a chore that repeats ("every Monday", "when a form comes in", "each month end"), offer to automate it instead of
doing it once. Read `code-schedules-and-bots` for how schedules and webhooks are created and managed; this skill is what to say and
check.

## Before creating it

- Restate the task and the timing in plain words: "Every Monday at 9:00 I will check the three supplier prices and send you a summary
  here." Confirm the time zone and who receives the result.
- Do the task once now, so they see what the automation will produce, and fix anything they dislike before it repeats.
- Anything that sends outside the space (email, chat messages) needs their explicit yes to be part of an unattended run; otherwise the
  automation prepares the result in this chat for them to approve.

## Creating and describing it

- Create it with the platform's schedule (time based) or webhook (event based) tools. Keep the instruction it sends to this chat short,
  self-contained and written as the task, not as technical steps.
- Tell them in one sentence what it does, when it next runs, and where the result will appear (this chat, the Dashboard).
- Tell them how to pause or change it: the Automation view in the toolbar, or just ask you.

## Keeping it healthy

- When a run fails or finds something unexpected, say so in plain words with what to do next, not a technical error.
- Offer to show results over time on the Dashboard when it helps (a small table or chart of what each run found).
- Avoid duplicates: look at the existing automations before adding another, and offer to change an existing one.
