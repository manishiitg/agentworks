# Timing and execution

Use the current schedules schema; do not edit schedules.json by shell or generic file tools. Website, Builder and MCP use the same saved definitions and server clock. List before editing and preserve omitted settings.

- Recurring callers use a five-field cron and an IANA timezone. For **every 5 minutes**, use cron_expression="*/5 * * * *"; every hour is "0 * * * *". in_minutes:5 means **once in five minutes**, never every five minutes. Confirm the saved timing_summary after create/update, and never describe a dated schedule as recurring. Calendar callers use explicit dates/time and may carry per-date input. For a one-time reminder, use the admitted in_minutes/run_at or calendar fields. Check an already-finished task now before creating an unnecessary reminder. Keep reminders the person requested unless they ask to remove them.
- A Crew message goes to its saved stable tab; a timer created within a side tab defaults there. An isolated destination is that timer’s own persistent conversation. Preserve the destination instead of silently switching it to main.
- A Goal can run its saved plan/routes/groups, or send its saved custom message sequence. Preserve every message and its order; do not convert a message schedule into a function or planning mode. Preserve an existing after condition, including outcome, delay and explicit deadline.
- There is one durable clock. Missed ticks are not replayed on restart. The existing two-minute late-slot admission limit remains. Global/product pauses and disabled flags still apply.
- A busy destination keeps the latest waiting occurrence per schedule. A newer occurrence replaces that schedule’s older waiting one. Distinct schedules sharing a destination take turns, oldest due first. Accepted waiting work has no max-start expiry.

Use run_now only for a requested real run. Read runs and the actual terminal outcome; saved next_run or an accepted receipt does not prove completion. Report global/product pauses, current blockers and the relevant timestamps separately from old skipped history. Never resume paused Goals merely to verify a refactor.
