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
    - get_ui_state
    - perform_ui_action
    work:
    - execute_shell_command
    - get_ui_state
    - perform_ui_action
  external-tools:
  - runs
  - sessions
---

# Runs

Run work, inspect results and logs, or find a saved chat. Read the procedure for the current product.

Start with the person's request and the current product. Read only the relevant procedure below: in product chat use `read_skill` with this skill name as `skill_name` and the listed `path` (chat has no `help action=topic`); only an MCP client uses `help action=topic` with this name as `topic` and the listed `path`. Use the actions offered by the current tool catalog. A guide does not grant access.

- Workflow runs: read `python-workflows` for starting a Python script with its saved preset or typed inputs. Its `@step` labels are not separate run targets.
- Run logs: read `references/run-logs/SKILL.md` when needed.
- Chat history: read `references/chat-history/SKILL.md` when needed.

Verify the saved result or returned run result, then give the person one clear next step. Keep their existing notes and omitted settings.
