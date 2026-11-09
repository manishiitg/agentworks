---
name: schedules
description: Create, change or inspect timed project callers.
---

# Schedules

Use `automations` with `scope=schedule` and only the actions listed in this chat or connection. Start with `automations action=list scope=schedule`. In MCP pass project (ID or name); product chat already binds its own project. Preserve IDs, timezone, enabled state and settings the person did not ask to change.

Use create/update/delete, enable/disable, run_now/stop and runs where offered. Repeating requests ("every N minutes/hours") use cron_expression and timezone; every 5 minutes is "*/5 * * * *". in_minutes/run_at and dated calendar entries are one-time only. State the reply's timing_summary so the person hears what was actually saved. MCP create/update take schedule with the advertised timing and action fields. A Crew schedule either sends a message to an agent or runs the Crew's Python workflow directly. New message schedules default to their own persistent chat; offer an existing chat when the person prefers it, and ask which destination if unstated. Preserve existing saved destinations. Workflow schedules use action `run` and a workflow target with typed inputs and optional saved settings; supply required inputs before enabling. Existing standalone workflow timers require conversion into Crew schedules, not a label change. Old plan files cannot run. The server stamps the caller and decides the next eligible time.

Read [timing and execution](references/operations.md) before changing clock/dependency settings or diagnosing a missed occurrence.
