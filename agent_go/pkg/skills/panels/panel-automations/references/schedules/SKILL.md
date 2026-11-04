---
name: schedules
description: Create, change or inspect timed project callers.
---

# Schedules

Use `automations` with `scope=schedule` and only the actions listed in this chat or connection. Start with `automations action=list scope=schedule`. In MCP pass project (ID or name); product chat already binds its own project. Preserve IDs, timezone, enabled state and settings the person did not ask to change.

Use create/update/delete, enable/disable, run_now/stop and runs where offered. Repeating requests ("every N minutes/hours") use cron_expression and timezone; every 5 minutes is "*/5 * * * *". in_minutes/run_at and dated calendar entries are one-time only. State the reply's timing_summary so the person hears what was actually saved. MCP create/update take schedule with the advertised timing and action fields. Distinguish a Crew chat timer (sends its message to the saved tab) from a timer on an owned workflow or script. The child runs its saved work and records start/result updates in its saved Crew chat, Main by default; direct Python needs no model turn. Supply required inputs, using the shared definitions and optional preset/fixed values. Existing standalone workflow timers retain their saved plan or custom message behavior; do not convert them by changing a label. The server stamps the caller and decides the next eligible time.

Read [timing and execution](references/operations.md) before changing clock/dependency settings or diagnosing a missed occurrence.
