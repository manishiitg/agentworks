---
name: work-skills
description: Discover, install, import, create, select, and remove reusable skills in {{product}}. Use when the user asks to add a capability or manage the skills available to a project or account.
---

# {{product}} skills

## Decide whether this belongs in memory or a skill

- Use root `MEMORY.md` for project-specific truths: verified facts,
  preferences, decisions, constraints, corrections, and durable context. Test
  the content with “{{product}} should remember that…”.
- Use `skills/<skill-name>/SKILL.md` for a reusable procedure: when it applies,
  ordered actions, checks, tools, expected output, and failure handling. Test
  it with “When asked to do X, {{product}} should…”.
- If both are relevant, keep the fact in memory and the procedure in the skill,
  then link them rather than duplicating instructions.
- Put temporary status, raw chat, guesses, secrets, and reliably retrievable
  live information in neither.
- Memory may be updated proactively after stable verification. A skill changes
  only when the user explicitly asks to preserve, create, or improve a reusable
  procedure.

- Use `list_skills` to inspect what is already installed before searching or
  creating another skill. Use `search_skills` to discover an existing skill.
- Use `install_skill` for a discovered skill and `import_skill` for a supplied
  skill source. Installation is account-level; selecting it for this project is
  a separate action. Call `update_project_skill_selection(action="select",
  skill="folder-name")` with the exact folder returned by `list_skills`.
  Use `action="deselect"` to remove only this project's selection. The same
  selection remains editable in **Setup > Skills**.
- When the user explicitly asks to preserve or improve a repeatable procedure,
  create or update a focused custom skill inside this {{product}} project at
  `skills/<skill-name>/SKILL.md`. This is project-local durable knowledge, like
  the root `MEMORY.md`; never write it into the account-wide `skills/custom/`
  library. Inspect the names and descriptions of every existing project skill
  before choosing a destination, then select the skill for this project with
  `update_project_skill_selection` so the runtime can load it.
- Update an existing skill only when the new knowledge has the same topic and
  would be loaded for the same kind of future request. Create a separate,
  clearly named skill when the topic, external system, audience, trigger, or
  intended outcome differs. A single request may create or update several
  skills when it contains independently reusable topics.
- Do not use one custom skill as general project memory, append unrelated notes
  to the most recently edited skill, or combine topics merely because they were
  discussed in the same conversation. If an existing skill has already become
  a catch-all, split the relevant material into focused skills when updating it.
  Keep the normal {{product}} identity; skill authoring is a capability, not a
  separate chat persona.
- A custom skill needs concise YAML frontmatter with `name` and `description`,
  followed by only the non-obvious instructions that improve future work. Add
  scripts or references only when they provide concrete reusable value. Keep
  `SKILL.md` small: target 150 lines or fewer. Move detailed examples,
  background explanations, schemas, and lookup tables into focused files under
  `references/`; move repeatable executable logic into `scripts/`. If the core
  instructions still grow beyond that target, split the skill by topic or
  trigger instead of making the main file longer.
- Never store credentials or secret values in a skill. Document required secret
  names and use the Secrets system at runtime.
- Before removing a skill, identify the exact installed name and explain that
  `uninstall_skill` removes the account-level installation, not only this
  project's selection.
- Load and follow a selected skill when its description matches the task. A
  skill does not grant filesystem, secret, network, MCP, or tool permissions
  beyond the current user and project.
