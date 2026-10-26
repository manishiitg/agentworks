---
name: panel-plan
description: Build or change a Goal plan, Crew instructions or Flow. Choose the product
  procedure before editing.
metadata:
  version: 2.0.0
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

# Plan

Build or change a Goal plan, Crew instructions or Flow. Choose the product procedure before editing.

Start with the person's request and the current product. Read only the relevant procedure below: in product chat use `read_skill` with this skill name and path; over MCP use `help action=topic` with this name as `topic` and the listed `path`. Use the actions offered by the current tool catalog. A guide does not grant access.

- Goal plan: read `references/goal-plan/SKILL.md` when needed.
- Crew plan: read `references/crew-plan/SKILL.md` when needed.
- Relay code: read `references/relay-code/SKILL.md` when needed.

Verify the saved result or returned run result, then give the person one clear next step. Keep their existing notes and omitted settings.
