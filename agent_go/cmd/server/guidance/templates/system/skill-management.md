---
name: "skill-management"
description: "Skill lifecycle and attachment model: workflow-selected skills are discovery context only, per-step enabled_skills is the runtime attachment, workflow-local skills carry reusable how-to guides. Load before reasoning about skills."
external: true
external_tools: ["crew_skills", "list_skills", "search_skills", "list_workflow_knowledge", "read_workflow_knowledge"]
external_note: "External mapping: discover current tool schemas before acting. Use plan action=list for native plan and step operations; read get_plan for expected_revision, then pass a stable request_id and listed native arguments. Internal operation names are plan action values, not standalone external tools. Settings uses settings get/update. For skill wiring, use list_workflow_knowledge and read_workflow_knowledge, Crew package actions and Brain skill packages under their existing roles and file/folder grants. Fetch referenced guidance by its listed topic name. Server-only scripts, bridge variables and internal URLs are not external client credentials."
---
## Skill Management — install skills and attach them to workflows

Skills are reusable instruction sets (a `SKILL.md` plus optional bundled files) injected into step agents at runtime only when the step explicitly enables them. This doc covers the full lifecycle: find → install → attach to the main chat → enable per step → remove. To *author* a new skill from scratch, use the `skill-creator` skill (install `anthropics/skills@skill-creator` in this workspace first if missing) — do not hand-write `SKILL.md` content.

### Where skills live

Skills belong to the **current product or workflow**, `<current-workspace>/skills/<folder>/SKILL.md`. There is no shared account skills library. `list_skills` also discovers skills installed by native agents in this workspace's `.skills/` and provider skill folders such as `.agents/skills` and `.claude/skills`. Installing in one workspace does not install in another.

### Lifecycle

1. **Find**
   - `list_skills` — installed skills in this workspace.
   - `search_skills(query)` — search the public registry.
2. **Install**
   - `install_skill(source)` — from the registry, source format `owner/repo@skill-name`.
   - `import_skill(github_url)` — from a GitHub folder URL.
   - Both download into `<current-workspace>/skills/<folder>/`. If a folder exists but has no `SKILL.md`, reinstall it with the same method it was originally installed with — **never write `SKILL.md` by hand**.
3. **Select for workflow/builder context**
   - `settings(action="update", skills={add:["folder-name"]})`. This records the skill as a selected workflow capability for workshop/builder discovery. Do NOT edit `workflow.json` manually.
4. **Enable for specific runtime steps**
   - `update_step_config(step_id, enabled_skills=["skill-a"])` makes that step receive the listed skills at execution time.
   - Step execution does not inherit workflow-selected skills, so every runtime skill must be listed on the step that needs it.
5. **Remove from the workflow**
   - `settings(action="update", skills={remove:["folder-name"]})`.
6. **Uninstall**
   - `uninstall_skill(folder_name)` — deletes its files and clears its main-chat and per-step attachments in this workspace. Platform-managed skills cannot be uninstalled.

Inspect at any point with `settings action=get` (the workflow's selected skills) and `list_skills` (everything installed). Inspect `planning/step_config.json` or use step config tools to see per-step `enabled_skills`.

### Attachment model: selected vs enabled (important)

There is **no cascade** from workflow-selected skills to runtime step execution. A step's effective skills are resolved as:

- Step has `enabled_skills` set to one or more folder names → the step receives exactly those skills.
- Step has no `enabled_skills` → the step receives no installed skills.

So workflow selection is discovery/context for workshop agents, not runtime inheritance. To remove runtime skills from a step, use `update_step_config(step_id, clear_fields=["enabled_skills"])` or set the desired replacement list explicitly.

**Workflow-specific know-how** belongs in a workflow-local skill, selected only on the steps that need it. Keep the index short and detail in references. There is no automatic global learning attachment after contract 1.0.51. Use the explicit `migrate_workflow_skills` upgrade for existing guides; it uses the same loader and selection fields, preserves source contents and history, and does not populate a shared skills library.

### When to use workflow-level vs per-step

- **Workflow-level (`add_skills`)** — capability inventory for builder/workshop review and discovery. Use this to document that a workflow is expected to use a skill, but still enable it on runtime steps.
- **Per-step (`enabled_skills`)** — the actual runtime attachment. Each attached skill costs prompt tokens and dilutes the agent's focus, so scope narrowly to the steps that need the skill.

### Troubleshooting

- **Skill not taking effect on a step** → check that the step explicitly lists it in `enabled_skills`. `settings(action="update", skills={add:[...]})` alone is not enough for runtime execution.
- **Folder exists but no `SKILL.md`** → reinstall via the original method (`install_skill` / `import_skill`); do not author the file manually.
- **Too many skills / bloated step prompts** → remove unnecessary skills from each step's `enabled_skills`; keep workflow-level selection as a lightweight capability inventory only when useful.
- **Need to author or improve a skill** → use the `skill-creator` skill; for sub-agent templates use `subagent-creator`. Neither installs/attaches — that's this doc's tools.
