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

Save ordinary shared artifacts under `$CREW_OUTPUT_DIR` (`<crew>/outputs/`),
which has the same location for the owner and other callers. `CREW_RUN_DIR`
is its compatibility name. For a structured function, use the execution’s
`FUNCTION_OUTPUT_DIR` and, when a result schema is declared,
`FUNCTION_RESULT_FILE`; those per-call files are read through call results.
Avoid choosing a different output path based on who called the Crew.

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

Registered external agents of your account are discoverable with `list_message_agents` when listed. Use `send_message` with `target="agent:<name>"` to initiate contact. They poll their inbox; a stored receipt does not wake the client or prove completion. Existing inbox replies remain optional.

Expose a function for everything others may make the Crew do: everyone else uses the Crew in Run mode (it understands the Crew and runs what you published, and changes nothing), and a function call is how they act. Your own chat is Builder mode, with full access.

## Watchers

For immediate event-driven checks, use `check_mode: "event"`; use `"both"` for events plus a backup `cron_expression` (at least five minutes apart). Omit `check_mode` for existing schedule-only behavior. Select an existing granted secret name in `secrets` and `event_secret_name`; `event_auth_mode` is `bearer` (default) or `github` for GitHub HMAC signatures. Management returns `event_path` on the existing product webhook receiver. Connect the source to that URL using the selected secret without printing its value. The source must support authenticated webhook notifications; otherwise keep scheduled checks. Incoming JSON is only a wake signal: the Python detector still queries/paginates the source and uses its saved state. Accepted wakes survive restart; bursts coalesce into checks at least five seconds apart. One time still stops after the first accepted matching action. In Watchers, use **Connect events in chat** or **Edit event connection**; the pane shows the event URL and secret name.

Chat setup and editing stay in Builder mode. Existing Run views stay unchanged; caller attribution follows product access. Configuring a Watcher never edits the shared product.

Read work-schedules-and-bots for condition-based calls. Use manage_condition_triggers in this Crew to create a person-owned caller attached to a published function, defaulting to this Crew. Pass script_content so the detector stays in the creator's own watcher folder with their selected Vault credentials. Use once=true for a specific wait that triggers once and disables after acceptance; omit it to keep watching. List first and use expected_revision on edits. The caller never borrows the Crew's credentials or reads its internals; the existing function keeps its own execution rules. Watchers are in Automation and Ctrl/Cmd+K.
