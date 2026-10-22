---
name: panel-run-logs
description: Run logs panel in Goals and Crews. Read a Goal’s executions, step progress and failure evidence.
metadata:
  panel-tools:
    agentworks:
    - list_executions
    - query_step
    - get_schedule_runs
    - execute_shell_command
  external-tools:
  - list_executions
  - query_step
---

# Run logs

Use list_executions, query_step, get_schedule_runs and the scoped saved run/log files. Select the correct time, run and step before diagnosing. Quote bounded relevant evidence without secret values or other people’s private chats. Distinguish queued/running/failed/completed from a successful HTTP request; do not re-run a failed step just to read its logs.
