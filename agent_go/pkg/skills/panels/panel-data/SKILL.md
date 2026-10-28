---
name: panel-data
description: Read or change files, tables, Brain knowledge, memory and skills. Keep
  the work in its current product.
metadata:
  version: 2.0.0
  panel-tools:
    work:
    - execute_shell_command
    - files
    - database
    - brain_browse
    - brain_read
    - brain_update
    - brain_skills
    - brain_access
    - list_skills
    - search_skills
    - import_skill
    - install_skill
    - uninstall_skill
    - update_project_skill_selection
    - settings
    agentworks:
    - execute_shell_command
    - files
    - database
    - brain_browse
    - brain_read
    - brain_update
    - brain_skills
    - brain_access
    - list_skills
    - search_skills
    - import_skill
    - install_skill
    - uninstall_skill
    - settings
    - search_playbooks
    relays:
    - execute_shell_command
    - files
    - brain_browse
    - brain_read
    - brain_update
    - brain_skills
    - brain_access
    - list_skills
    - search_skills
    - import_skill
    - install_skill
    - uninstall_skill
    - settings
  external-tools:
  - files
  - database
  - brain
  - skills
---

# Data

Read or change files, tables, Brain knowledge, memory and skills. Keep the work in its current product.

Start with the person's request and the current product. Read only the relevant procedure below: in product chat use `read_skill` with this skill name as `skill_name` and the listed `path` (chat has no `help action=topic`); only an MCP client uses `help action=topic` with this name as `topic` and the listed `path`. Use the actions offered by the current tool catalog. A guide does not grant access.

- Files: read `references/files/SKILL.md` when needed.
- Database: read `references/database/SKILL.md` when needed.
- Brain: read `references/brain/SKILL.md` when needed.
- Knowledgebase: read `references/knowledgebase/SKILL.md` when needed.
- Memory: read `references/memory/SKILL.md` when needed.
- Skills: read `references/skills/SKILL.md` when needed.
- Playbooks: read `references/playbooks/SKILL.md` when needed.

Verify the saved result or returned run result, then give the person one clear next step. Keep their existing notes and omitted settings.
