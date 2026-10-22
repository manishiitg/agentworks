---
name: panel-skills
description: Skills panel in Goals and Crews. Inspect sources, add, select, create or remove a project skill.
metadata:
  panel-tools:
    work:
    - list_skills
    - search_skills
    - import_skill
    - install_skill
    - uninstall_skill
    - update_project_skill_selection
    - settings
    agentworks:
    - list_skills
    - search_skills
    - import_skill
    - install_skill
    - uninstall_skill
    - settings
    relays:
    - list_skills
    - search_skills
    - import_skill
    - install_skill
    - uninstall_skill
    - settings
  external-tools:
  - crew_skills
  - list_skills
  - search_skills
  - install_skill
  - import_skill
  - uninstall_skill
---

# Skills

Use list_skills/search_skills before install_skill/import_skill/uninstall_skill. Inspect source/lock metadata; an inventory never proves who installed it. Remove only the requested package. Crew selection uses update_project_skill_selection; Goal selection uses settings action=update with the current version. For a new reusable procedure, create a focused skills/<name>/SKILL.md using scoped file tools, then list it to verify. Platform skills and user-installed skills are different inventories.

For skill sources, installation, memory versus procedures and focused skill authoring, Read [the detailed procedure](references/authoring.md).
