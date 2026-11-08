---
name: "skill-management"
description: "Skill lifecycle and attachment model: workflow-selected skills are discovery context only, agent(skills=...) is the runtime attachment, workflow-local skills carry reusable how-to guides. Load before reasoning about skills."
external: true
external_tools: ["crew_skills", "list_skills", "search_skills", "list_workflow_knowledge", "read_workflow_knowledge"]
---
## Skill Management — install skills and attach them to workflows

Skills are reusable instruction sets (a `SKILL.md` plus optional bundled files) injected into step agents at runtime only when the step explicitly enables them. This doc covers the full lifecycle: find → install → attach to the main chat → enable per step → remove. To *author* a new skill from scratch, use the `skill-creator` skill (install `anthropics/skills@skill-creator` in this workspace first if missing) — do not hand-write `SKILL.md` content.

### Where skills live

Skills belong to the **current product or workflow**, `<current-workspace>/skills/<folder>/SKILL.md`. There is no shared account skills library. `skills action=list` also discovers skills installed by native agents in this workspace's `.skills/` and provider skill folders such as `.agents/skills` and `.claude/skills`. Installing in one workspace does not install in another.

### Lifecycle

1. **Find**
   - `skills action=list` — installed skills in this workspace.
   - `skills action=search` (query) — search the public registry.
2. **Install**
   - `skills action=install` (source) — from the registry, source format `owner/repo@skill-name`.
   - `skills action=import` (github_url) — from a GitHub folder URL.
   - Both download into `<current-workspace>/skills/<folder>/`. If a folder exists but has no `SKILL.md`, reinstall it with the same method it was originally installed with — **never write `SKILL.md` by hand**.
3. **Select for workflow/builder context**
   - `settings(action="update", skills={add:["folder-name"]})`. This records the skill as a selected workflow capability for workshop/builder discovery. Do NOT edit `workflow.json` manually.
4. **Give a script's agent a skill**
   - `agent(name, task, skills=("skill-a",))` makes that agent receive the listed skills for that call. Agents do not inherit workflow-selected skills, so every skill must be listed on the call that needs it.
5. **Remove from the workflow**
   - `settings(action="update", skills={remove:["folder-name"]})`.
6. **Uninstall**
   - `skills action=uninstall` (folder_name) — deletes its files and clears its main-chat and per-step attachments in this workspace. Platform-managed skills cannot be uninstalled.

Inspect at any point with `settings action=get` (the workflow's selected skills) and `skills action=list` (everything installed).

### Attachment model: selected vs given (important)

There is **no cascade** from workflow-selected skills to the agents a script runs. An `agent()` call receives exactly the skills named in its `skills=` argument, and none when it names none. Workflow selection is discovery context for Builder chat, not runtime inheritance. Older plan workflows keep their saved per-step skills and are not edited.

**Workflow-specific know-how** belongs in a workflow-local skill, selected only on the steps that need it. Keep the index short and detail in references. There is no automatic global learning attachment after contract 1.0.51. Use the explicit `migrate_workflow_skills` upgrade for existing guides; it uses the same loader and selection fields, preserves source contents and history, and does not populate a shared skills library.

### When to use workflow-level vs per-step

- **Workflow-level (`add_skills`)** — capability inventory for builder/workshop review and discovery. Use this to document that a workflow is expected to use a skill, but still enable it on runtime steps.
- **Per call (`agent(skills=...)`)** — the actual runtime attachment. Each attached skill costs prompt tokens and dilutes the agent's focus, so scope narrowly to the calls that need the skill.

### Troubleshooting

- **Skill not taking effect for an agent** → check that its `agent()` call lists it in `skills=`. `settings(action="update", skills={add:[...]})` alone is not enough for runtime execution.
- **Folder exists but no `SKILL.md`** → reinstall via the original method (`skills action=install` / `skills action=import`); do not author the file manually.
- **Too many skills / bloated agent prompts** → remove unnecessary skills from each `agent()` call's `skills=`; keep workflow-level selection as a lightweight capability inventory only when useful.
- **Need to author or improve a skill** → use the `skill-creator` skill; for sub-agent templates use `subagent-creator`. Neither installs/attaches — that's this doc's tools.
