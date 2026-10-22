---
name: panel-goal-execution
description: Run a Goal panel. Start, stop or inspect real Goal execution and its individual steps.
metadata:
  panel-tools:
    agentworks:
    - execute_step
    - debug_step
    - run_full_workflow
    - query_step
    - list_executions
    - stop_step
    - stop_all_executions
---

# Run a Goal

Read builder-reference/references/running-steps.md and execution-policy.md for the current Goal contract. Use execute_step/debug_step for a saved step, run_full_workflow for the requested full Goal, query_step/list_executions for results, and stop_step/stop_all_executions for the selected active work. Discover route/group inputs before running. Runs perform real effects; do not simulate or run twice merely because the first response was slow. Verify actual status/output. Crew chat and Relay calls have separate execution contracts.
