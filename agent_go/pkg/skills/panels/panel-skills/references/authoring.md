# project skills

## Save a browser procedure from chat

A request such as “Save how we did that as a reusable skill” uses the normal
project skill tools and file access. Inspect the existing skills, then create or
update `skills/<name>/SKILL.md` with the verified steps, variable inputs, expected
result and what to do when the website changes. Use the project's existing
browser tools; **Take control** lets the person sign in or complete a manual
step. A video alone does not prove the procedure works. Preserve only useful
instructions, never passwords, cookies or secret values. Read back the file,
list the skill, and select it using this product's existing selection tool when
available. Explain any untested steps plainly. There is no separate browser
teaching or publish requirement.

## What the inventory means

Custom skills are extra files in this workspace, not proof that the person
installed each one. A single repository import can supply many skill folders,
including its developer guides. Before attributing an installation, inspect
the recorded source in the inventory and `skills-lock.json`; use saved tool
history or conversation evidence to identify who requested it. A source or
file timestamp alone cannot identify the installer. Do not uninstall a whole
package because one guide looks unrelated; inspect usage and the person's
requested scope first.

## Decide whether this belongs in memory or a skill

- Use root `MEMORY.md` for project-specific truths: verified facts,
  preferences, decisions, constraints, corrections, and durable context. Test
  the content with “project should remember that…”.
- Use `skills/<skill-name>/SKILL.md` for a reusable procedure: when it applies,
  ordered actions, checks, tools, expected output, and failure handling. Test
  it with “When asked to do X, project should…”.
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
  skill source. Installation stays in the current workspace; selecting it for an agent is
  a separate action. For Crew, call
  `update_project_skill_selection(action="select", skill="folder-name")` with
  the exact folder returned by `list_skills`; `action="deselect"` removes only
  this project's selection. For a Goal, update its selected skills through
  `settings action=update` using the current settings version. The same
  selection is managed through chat. **Build > Skills** (or Recent) shows
  installed custom skills. In a Crew, owners can open a skill in Files to edit
  it, or uninstall it from this panel; selection is managed through chat.
  Crew-local copies of another product's skills are ordinary project files:
  edit or remove those copies without changing the original product. Only
  the current product's built-in platform guides remain protected.
- When the user explicitly asks to preserve or improve a repeatable procedure,
  create or update a focused custom skill inside this project project at
  `skills/<skill-name>/SKILL.md`. This is project-local durable knowledge, like
  the root `MEMORY.md`; keep skills scoped to this workspace. Inspect the names and descriptions of every existing project skill
  before choosing a destination, then select the skill with this product's existing settings or selection tool
  so the runtime can load it.
- Update an existing skill only when the new knowledge has the same topic and
  would be loaded for the same kind of future request. Create a separate,
  clearly named skill when the topic, external system, audience, trigger, or
  intended outcome differs. A single request may create or update several
  skills when it contains independently reusable topics.
- Do not use one custom skill as general project memory, append unrelated notes
  to the most recently edited skill, or combine topics merely because they were
  discussed in the same conversation. If an existing skill has already become
  a catch-all, split the relevant material into focused skills when updating it.
  Keep the normal project identity; skill authoring is a capability, not a
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
  `uninstall_skill` removes the current workspace's files and clears its
  main-chat and workflow-step attachments. Identify affected agents first.
  Other workspaces retain their own copies.
- Load and follow a selected skill when its description matches the task. A
  skill does not grant filesystem, secret, network, MCP, or tool permissions
  beyond the current user and project.
