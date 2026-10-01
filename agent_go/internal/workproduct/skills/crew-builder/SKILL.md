---
name: crew-builder
description: Read before Crew setup, creating another Crew, looking up earlier conversation history, or coding/repository work; follow authorized project boundaries.
---

Use the Crew's existing role and purpose. Change setup only within the user's
request. Discover current skills, connections, secrets, schedules and functions
before making changes; use their dedicated tools and names, never secret values.
Load the relevant work-* feature skill before using its management tools.

In the private CLI runtime, `project/` links to the real Crew. Read, create,
rename and edit durable project files there; use `cd project && ...` for commands
that need project-relative paths. Bridge tools use Crew-relative paths without
`project/`. Preserve the project's own instructions and configuration. Generated
CLI prompts, skills and connection metadata belong in the private runtime.

Call other Crews through `list_functions` and `call_function` when authorized.
Keep results and caller conversations separate from the Crew's main human chat.
Review reader suggestions with the owner. Accepting a suggestion only records
the decision; implement the requested change explicitly and verify the result.

## How to work

- When asked for another Crew, use `create_crew` with the requested name and
  icon (defaults to the name's initial). It creates a separate persistent project.
- For attached files, workflow references, or calls to another Crew/workflow,
  read `work-workflow-files` before acting. For an incoming function call,
  follow that skill's progress and result contract.
- Before answering about earlier work, search this Crew's saved conversations
  by keyword or date: owner history in `builder/conversation/`, other users'
  history in `builder/crew-chats/users/<user>/` (JSON fields
  `conversation_history[].Role` and `.Parts[].Text`). Your recall may be
  incomplete after restart. Other Crews' `builder/` chats and other products'
  chats are unreadable even when Crew files are shared; reach them via tools.
- Answer conversational requests directly. Use research, MCP, skills, files,
  browser, and terminal when helpful; inspect available tools and data before
  claiming access. Save reusable outputs as clear project artifacts.
- State consequential assumptions; ask only for choices that materially
  change the result. Continue within the user's request and permissions.
- Load relevant attached skills; they guide work but grant no extra access.
  When explicitly asked to save a procedure, inspect existing descriptions
  and update only a skill with the same topic and trigger. Create separate
  skills for different topics, audiences, systems, or outcomes. Keep SKILL.md
  operational and short; move long examples and tables to supporting files.
  Never build a catch-all memory skill or append unrelated notes.

## Memory versus skills

- `MEMORY.md` holds verified project facts, preferences, decisions, constraints,
  corrections, and durable context: "Crew should remember that…".
- `skills/<skill-name>/SKILL.md` holds reusable procedures, triggers, steps,
  checks, tool usage, outputs, and failure handling: "When asked to do X, Crew should…".
- Use both only when needed; memory links to the skill that owns a procedure.
  Do not copy the same instructions into both files. Neither stores temporary
  status, raw chats, guesses, secrets, or reliably retrievable information.
- Save stable verified memory proactively. Create or change skills only on an
  explicit request to preserve, create, or improve a reusable procedure.

## Coding rules

- Inspect the project and its instruction files before editing. Preserve user
  changes, conventions, and the smallest useful scope; reuse existing components.
- New source and applications go under `code/`; preserve established layouts.
  Project metadata, documentation, and managed folders may remain at the root.
- Clone repositories and create worktrees inside this Crew's `code/` folder
  (`code/<repo>` or `code/worktrees/<branch>`), never `/tmp` or elsewhere.
  Outside work is invisible to later turns, callers, and file tools, and is
  lost on server restart. For a commit on a branch other than the checked-out
  one, report the repository, branch, and path.
- Implement complete behavior unless asked for a sketch. Validate proportionally
  with relevant tests, types, builds, or execution before reporting success.
  Explain outcomes and material tradeoffs without dumping raw output.
