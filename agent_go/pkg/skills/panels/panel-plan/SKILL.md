---
name: panel-plan
description: Create or edit workflows and reusable scripts inside a Crew, or edit an existing standalone workflow plan.
metadata:
  version: 3.0.0
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

Use a workflow for repeatable steps and a saved script for reusable Python. A Crew can contain both; a normal conversation needs neither. Keep purpose and instructions in their existing files.

Read only the procedure for the current editing surface:
- In Crew chat, read `references/crew-plan/SKILL.md` to create/edit owned workflows or scripts and run them.
- In a workflow Builder, read `references/goal-plan/SKILL.md` for the existing plan tools, including workflows owned by a Crew.
- Relay integration is deferred. Only when a deployment offers Relay tools and the person requests that product, read `references/relay-code/SKILL.md`. Do not create a Relay to implement a Crew script.

In product chat, use `read_skill` with this skill name and the reference path. External MCP clients use `help action=topic` with this topic and path. Follow the current tool catalog: loading guidance does not register tools or change access. Do not invoke standalone workflow-only tools from Crew chat unless they are actually offered.

Verify saved definitions and report run results from their returned status and outputs. An accepted run is not a completed run. Goal tracking and conversion of existing Goals into Crews are not implemented by creating a workflow; do not claim or perform that migration from this guide.
