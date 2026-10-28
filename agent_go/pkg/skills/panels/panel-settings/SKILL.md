---
name: panel-settings
description: Change project settings or models, inspect usage, or use the browser,
  terminal and a connected computer.
metadata:
  version: 2.0.0
  panel-tools:
    work:
    - set_work_identity
    - manage_custom_commands
    - execute_shell_command
    - settings
    - models
    - costs
    - manage_laptop_access
    - perform_ui_action
    - browser_connections
    - agent_browser
    agentworks:
    - manage_custom_commands
    - execute_shell_command
    - manage_project
    - settings
    - models
    - costs
    - manage_laptop_access
    - browser_connections
    - agent_browser
    - get_contract_upgrades
    - set_workflow_contract_version
    - migrate_workflow_skills
    relays:
    - manage_custom_commands
    - execute_shell_command
    - manage_project
    - settings
    - models
    - costs
    - browser_connections
    - agent_browser
  external-tools:
  - settings
  - workspace
  - models
  - settings.chat
  - costs
  - terminal
  - browser
  - workflow_upgrade
---

# Settings

Change project settings or models, inspect usage, or use the browser, terminal and a connected computer.

Start with the person's request and the current product. Read only the relevant procedure below: in product chat use `read_skill` with this skill name as `skill_name` and the listed `path` (chat has no `help action=topic`); only an MCP client uses `help action=topic` with this name as `topic` and the listed `path`. Use the actions offered by the current tool catalog. A guide does not grant access.

- Settings: read `references/settings/SKILL.md` when needed.
- Models: read `references/models/SKILL.md` when needed.
- Costs: read `references/costs/SKILL.md` when needed.
- Local laptop: read `references/local-laptop/SKILL.md` when needed.
- Terminal: read `references/terminal/SKILL.md` when needed.
- Browser: read `references/browser/SKILL.md` when needed.
- Upgrades: read `references/upgrades/SKILL.md` when needed.

Verify the saved result or returned run result, then give the person one clear next step. Keep their existing notes and omitted settings.
