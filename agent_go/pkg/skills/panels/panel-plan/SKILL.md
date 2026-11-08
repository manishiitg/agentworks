---
name: panel-plan
description: Manage Crew workflows, reusable scripts and goal tracking, or edit an existing standalone workflow plan.
metadata:
  version: 3.1.0
  panel-tools:
    agentworks:
    - execute_shell_command
    - create_plan
    - add_step
    - update_step
    - maintain_plan
    - settings
    work:
    - execute_shell_command
    - files
    - crew_workflow_files
    - run_workflow_trigger
    - get_workflow_trigger_run
    relays:
    - settings
    - test_relay
    - get_relay_run
    - get_relay_releases
    - publish_relay
    - execute_shell_command
    - files
  external-tools:
  - plan
  - get_plan
  - relay
---

# Workflows and scripts

In a Crew, **Automations** contains repeatable work. Each Crew has one workflow connecting steps; routes select different tasks. A saved script runs reusable Python. To measure an outcome, use one dedicated agent and one workflow route that records the number; add a schedule only if asked, and ask before creating a dashboard. A normal chat needs none of this.

Use the tools offered in this chat:
- `crew_workflow_files`: list existing work, create a workflow/script, read it, or edit it. `describe` returns the exact schema for an editing operation. `edit` reuses the Builder's named-step and input operations; omitted steps stay unchanged. Use `write` for code or a saved script's definition, never to replace workflow plan/config/input files.
- `functions action=run_trigger`: run either kind with `inputs`. No function setup is required. Check the returned run with `functions action=trigger_status`; accepted does not mean completed.

Define inputs once, with types and optional defaults. A workflow with no inputs needs no input file or preset. Secrets belong in Vault. Reuse this Crew's saved agents when their role fits; do not copy another person's account or chat history.

For examples, open `references/crew-plan/SKILL.md` directly. In a workflow Builder, use its existing plan tools; `references/goal-plan/SKILL.md` contains examples. The backend validates edits even when no guide was read.

To run work on a schedule or in response to an event, use `panel-automations`. For logs, Inspect and costs, open the workflow's Runs view under Automations. Keep the conversation in its Crew chat.
