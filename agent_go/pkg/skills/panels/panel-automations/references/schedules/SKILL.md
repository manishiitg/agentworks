---
name: schedules
description: Create, change or inspect timed project callers.
---

# Schedules

Use `schedules` and only the actions listed in this chat or connection. Start with action=list. In MCP pass project (ID or name); product chat already binds its own project. Preserve IDs, timezone, enabled state and settings the person did not ask to change.

Use create/update/delete, enable/disable, run_now/stop and runs where offered. Repeating requests ("every N minutes/hours") use cron_expression and timezone; every 5 minutes is "*/5 * * * *". in_minutes/run_at and dated calendar entries are one-time only. State the reply's timing_summary so the person hears what was actually saved. MCP create/update take schedule with the advertised timing and action fields. A Crew timer sends its complete message to its saved tab (main by default); Goal timers run the saved plan or preserve their custom message sequence. Flows use their published function entry, not Goal schedules. The server stamps the caller and decides the next eligible time.

Read [timing and execution](references/operations.md) before changing clock/dependency settings or diagnosing a missed occurrence.
