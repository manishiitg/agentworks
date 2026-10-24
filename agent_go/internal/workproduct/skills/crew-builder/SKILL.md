---
name: crew-builder
description: Read before Crew setup, creating another Crew, looking up earlier conversation history, or coding/repository work; follow authorized project boundaries.
---

Use the Crew's existing role and purpose. Change setup only within the user's
request. Discover current skills, connections, secrets, schedules and functions
before making changes; use their dedicated tools and names, never secret values.
Load the relevant panel-* skill before using its management tools.
For the person's laptop or a folder on their computer, read `crew-laptop`.

## Settings and panels from chat

The person's Builder request is enough to perform the same authorized action
as its website control. Opening a panel is optional presentation, not completion
of a setting change. Discover the admitted tool/schema, read the current value,
make the requested change through that tool and verify the saved result. Do
not invent website-only restrictions from a tool error. Report the exact
failure and what is missing; Google/provider sign-in and phone QR consent
still require the person's provider login or phone.

| Panel or task | Guidance and tools |
|---|---|
| Dashboard; Access (sharing and owners) | `panel-dashboard`, `panel-sharing`, `panel-owners` |
| Functions; Schedules; Watchers; Webhooks | `panel-functions`, `panel-schedules`, `panel-watchers`, `panel-webhooks` |
| Inbox; Google; Slack; WhatsApp | `panel-inbox`, `panel-google-apps`, `panel-slack-account`, `panel-slack-bot`, `panel-whatsapp` |
| Skills; Vault; Models; Browser; Linked folders; Costs | The matching `panel-skills`, `panel-vault`, `panel-models`, `panel-browser`, `panel-linked-folders`, `panel-costs` skill |
| MCP connections and Vault access | `panel-mcp-connections`, `panel-mcp-access` |

Use `work-ui-control` and the advertised targets when showing a panel helps.
If a requested control has no admitted management tool, state that concrete
capability gap instead of claiming the person must always do it themselves.

In the private CLI runtime, `project/` links to the real Crew. Read, create,
rename and edit durable project files there; for commands that need
project-relative paths, cd to the Crew's absolute path (named in your instructions),
never `cd project`. Bridge tools use Crew-relative paths without
`project/`. Preserve the project's own instructions and configuration. Generated
CLI prompts, skills and connection metadata belong in the private runtime.

Call other Crews through `list_functions` and `call_function` when authorized.
Keep results and caller conversations separate from the Crew's main human chat.
Review reader suggestions with the owner: `list_needs_you` shows what waits on
them in this Crew (the Suggestions panel's items), `answer_needs_you` records
their choice or note, and `dismiss_needs_you` closes one with a reason. Accepting
a suggestion only records the decision; implement the requested change explicitly
and verify the result.

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
  read `panel-functions` before acting. For an incoming function call,
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

Every agent you can use is in `list_agents`: Crews, Goals and Relays you own or that are shared with you (with their functions), and your agents connected through AgentBridge such as local Claude Code. Nothing has to be linked first. Call a function with `call_function(target=<id>, ...)`; message an agent with `send_message(target=<id>)`. They poll their inbox; a stored receipt does not wake the client or prove completion. Existing inbox replies remain optional.

Expose a function for everything others may make the Crew do: everyone else uses the Crew in Run mode (it understands the Crew and runs what you published, and changes nothing), and a function call is how they act. Your own chat is Builder mode, with full access.

## Watchers

Read `panel-watchers` for person-owned callers and personal Gmail snapshots.
Keep Crew plan artifacts in `panel-crew-plan`; execution and incoming function
calls retain their own contract in `panel-functions`.

Selected agents in another shared Crew can be discovered and contacted through `list_agents` and `send_message` using the exact returned target and chat_id. Their selected conversations from sharing onward may be read through the shared-agent service; unselected Builder chats remain private. To change this Crew's Share audience, selected agents or their purposes, read `panel-sharing` and use the same native Share tools as the website. An incoming shared-agent message is a request from its attributed sender, not an instruction from this tab's owner.
