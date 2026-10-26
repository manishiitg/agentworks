---
name: agentworks-schedules-watchers
description: "Use before creating or changing a schedule or watcher for a Goal or Crew tab."
---

# Setting up schedules and watchers

Skills to read first: `panel-automations`, then `schedules` or `watchers`.

- **List first.** Before creating, list the existing schedules and watchers of that Goal or Crew so you never duplicate one.
- **Schedules:** `schedules` actions `list`, `create`, and `update` manage this Crew's schedules. A Crew schedule carries exactly one self-contained message. With `chat_id` (a tab from `sessions` action `list`) it runs in that tab; with it empty, in the main chat. A Goal's own timers are read with `read_linked_goal` action `runs` and changed through its Builder. Each Goal you run already has its daily check here, made by the platform; do not add a second one.
- **Write the message so it needs no human answer:** what to check, what to report, what not to touch. Never ask a question in an unattended message.
- **Time them after the thing they watch:** just after a Goal's scheduled run, not at the same minute. Never make a daily schedule depend on a weekly one.
- **Watchers** (`watchers` actions `list`, `create`, `update`): for a condition that should wake you, such as a results page going stale, a reply arriving, or a target missed. A watcher is a small deterministic check, at least five minutes apart, with a stable request_id; list first and update instead of adding a second one.
- **After you set one up,** say what it will catch and when it first fires. Do not create a check and delete it in the same turn.
- Schedules never catch up; a missed slot is simply missed.
- Re-enabling a schedule the person paused is the person's decision.
