---
name: panel-settings
description: Change how the app looks, project settings or models, inspect usage, or use the browser,
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
  external-tools:
  - settings
  - workspace
  - models
  - settings.chat
  - costs
  - terminal
  - browser
---

# Settings

Change how the app looks, project settings or models, inspect usage, or use the browser, terminal and a connected computer.

Start with the person's request and the current product. Read only the relevant procedure below: in product chat use `read_skill` with this skill name as `skill_name` and the listed `path` (chat has no `help action=topic`); only an MCP client uses `help action=topic` with this name as `topic` and the listed `path`. Use the actions offered by the current tool catalog. A guide does not grant access.

- Settings: read `references/settings/SKILL.md` when needed.
- Models: read `references/models/SKILL.md` when needed.
- Costs: read `references/costs/SKILL.md` when needed.
- Appearance (the person's own theme, colours, fonts, background): read `references/appearance/SKILL.md` before changing it.
- Local laptop: read `references/local-laptop/SKILL.md` when needed.
- Terminal: read `references/terminal/SKILL.md` when needed.
- Browser: read `references/browser/SKILL.md` when needed.
- Old upgrade records: read `references/upgrades/SKILL.md` only when explaining an existing historical record; it offers no migration/stamp action.
- Sharing a slash command with the Team (optional both ways): a command is only text. To share one, read it with the commands `list`, then `team_sync` `save_file` a JSON file `commands/<new id>.json` in the Team's shared folder with `{"kind":"command","name","description","prompt"}` (the prompt is the command's text; reuse your own earlier file to update it). To add a teammate's, find `commands/*.json` with `team_sync` `files` / `read_file`, show the person the prompt, and use the commands `create` under a name they don't already have (ask for another if it is taken). Never edit a command the person already has.

Verify the saved result or returned run result, then give the person one clear next step. Keep their existing notes and omitted settings.
