---
name: panel-goal-plan
description: Goal plan panel. Design, edit or inspect Goal steps, routes and dependencies.
metadata:
  panel-tools:
    agentworks:
    - execute_shell_command
    - create_plan
    - add_step
    - update_step
    - maintain_plan
    - get_workflow_config
---

# Goal plan

Inspect this product’s current plan and registered tool schemas before editing. The person’s Builder request authorizes the requested change under existing server checks. Verify the saved result.

Read builder-reference/references/plan-editing-tools.md and the existing plan/configuration. Use create_plan, add_step, update_step, maintain_plan and the admitted migration tools for the requested edit. Check affected data, prompts and downstream consumers once before testing. Do not put a Goal’s step graph into a Crew or Relay.
