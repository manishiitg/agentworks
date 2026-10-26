---
name: panel-runs
description: Run work, inspect results and logs, or find a saved chat. Read the procedure
  for the current product.
metadata:
  version: 2.0.0
  panel-tools:
    agentworks:
    - execute_step
    - debug_step
    - run_full_workflow
    - query_step
    - list_executions
    - stop_step
    - stop_all_executions
    - get_schedule_runs
    - execute_shell_command
    - manage_builder_chats
    - get_ui_state
    - perform_ui_action
    relays:
    - list_relay_runs
    - get_relay_run
    - get_relay_releases
    - manage_builder_chats
    - execute_shell_command
    - get_ui_state
    - perform_ui_action
    work:
    - execute_shell_command
    - get_ui_state
    - perform_ui_action
  external-tools:
  - runs
  - relay
  - sessions
---

# Runs

Run work, inspect results and logs, or find a saved chat. Read the procedure for the current product.

Start with the person's request and the current product. Read only the relevant procedure below: in product chat use `read_skill` with this skill name and path; over MCP use `help action=topic` with this name as `topic` and the listed `path`. Use the actions offered by the current tool catalog. A guide does not grant access.

- Goal execution: read `references/goal-execution/SKILL.md` when needed.
- Run logs: read `references/run-logs/SKILL.md` when needed.
- Relay runs: read `references/relay-runs/SKILL.md` when needed.
- Chat history: read `references/chat-history/SKILL.md` when needed.

Verify the saved result or returned run result, then give the person one clear next step. Keep their existing notes and omitted settings.
