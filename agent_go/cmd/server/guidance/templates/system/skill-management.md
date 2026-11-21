---
name: "skill-management"
description: "Create, install, select and scope reusable Crew skills and explicit agent-call attachments."
external: true
external_tools: ["crew_skills", "skills", "settings", "files"]
---
# Skills

Skills are reusable procedures: `skills/<name>/SKILL.md` plus optional supporting
files. Keep one topic and trigger per skill, a short index and progressive detail
in references. Verified project facts belong in memory; temporary status, raw
chats and secrets belong in neither. Read `panel-data`'s Skills reference and the
current schemas before changing a skill or selection.

List first. Installed skills belong to this Crew; inventory can also discover its
native provider skill folders. A discovered folder is not an account-wide skill
library and grants no permissions. Built-in platform skills remain managed.

- Find/install/import/uninstall with the admitted `skills` or `crew_skills`
  actions. Reuse the exact source/name returned. If an installed package lacks
  its SKILL.md, reinstall through its original source rather than inventing
  replacement upstream instructions.
- A person can request a custom skill. Read an existing same-topic skill first,
  then create or update `skills/<name>/SKILL.md` through authorized files tools,
  with `name` and a clear trigger description in YAML frontmatter. Load an
  available skill-creator guide when useful; installing another package is not
  a prerequisite for writing their own procedure. Preserve unrelated content
  and revision/request identities required by the tool.
- Select a skill for Crew/Builder discovery through `settings` or the current
  workspace update tool. Do not edit platform settings files manually. A saved
  selection is not proof an execution agent loaded the skill.
- A Python `agent(..., skills=("name",))` receives the explicitly named skills.
  It does not inherit every Crew/workflow selection. Scope each call to what it
  needs; read `python-workflows` before editing those call arguments.
- Remove a selection or uninstall only as requested. Platform-managed skills
  cannot be uninstalled. Inspect the saved selection and actual inventory after
  the change; do not claim an unrelated workspace was updated too.

No plan-step skill configuration, upgrade stamp or automatic learning phase is
needed for new work. Historical guides and source files remain evidence; do not
run retired migration tools or move private instructions into a shared catalog.
