---
name: crew-builder
description: Read for Crew panel or Ask AI guidance, project setup, reply-card buttons and saved commands, creating another Crew, earlier conversation history, or coding/repository work; follow authorized project boundaries.
---

Use the Crew's existing purpose. Change setup only within the user's
request. Discover current skills, connections, secrets, schedules and functions
before making changes; use their dedicated tools and names, never secret values.
Load the relevant panel-* skill before using its management tools.
For the person's laptop or a folder on their computer, read `crew-laptop`.

The sidebar’s New button opens the New project form directly. Home lets the
person ask their own AgentWorks, choose a use case or return to recent work.
Studio is the private shortcut using the existing Crew workspace;
it is not another product or permission. A Home use case only fills the request
until the person sends it.

## Settings and panels from chat

The person's Builder request is enough to perform the same authorized action
as its website control. Opening a panel is optional presentation, not completion
of a setting change. Discover the admitted tool/schema, read the current value,
make the requested change through that tool and verify the saved result. Do
not invent website-only restrictions from a tool error. Report the exact
failure and what is missing; Google/provider sign-in and phone QR consent
still require the person's provider login or phone.

| Panel or task | Read |
|---|---|
| Dashboards, TSX conversion, data/actions, motion or static export | `panel-dashboard` and its relevant authoring/motion reference |
| Files; Database | `panel-data` |
| Memory; Skills | `panel-data` |
| Chat history; named agents and their owners, models, pins or ordering | `panel-runs`, `crew-agents`, `panel-settings` |
| Share: people, Team, agents, Actions, Slack and dashboards | `panel-access` |
| Suggestions from people using this Crew | `panel-access` |
| Actions | `panel-dashboard`, `panel-access` |
| Schedules; Watchers; Webhooks | `panel-automations` |
| Email inbox or personal Gmail monitoring | `panel-channels`, `panel-channels`, `panel-automations` |
| Vault: apps, saved accounts, secret names and computers used here | `panel-access`; then `panel-access`, `panel-access`, `panel-channels`, `panel-channels` or `panel-settings` as needed |
| Shared Brain; Linked projects & folders | `panel-data`, `panel-access` |
| Browser and saving a reusable browser procedure | `panel-settings` |
| Slack bot for this Crew | `panel-access` |
| Personal Slack; WhatsApp | `panel-channels` |
| MCP access for external AI apps | `panel-access` |
| Connected agents (AgentBridge), Cloud or Laptop access | `panel-access`, `crew-agents` |
| Instructions & identity; Models; Costs & usage; Terminal | `panel-settings`, `panel-settings`, `panel-settings`, `panel-settings` |

Shared-folder clash (two people changed the same lines; both are notified): run
`team_sync action=clashes` to see both versions, `current` (the file now) and
`other` (the other person's, kept in history). Show the person the difference in
plain words, then do what they ask with `team_sync action=resolve path=… clash_id=…`:
"keep mine" / "keep theirs" → the side whose `who` matches (`keep=current` or
`keep=other`); "merge them" → write the combined text and pass it as `content`. Each is a new revision every copy downloads and closes the clash;
history is never erased. Over MCP it is `workspace action=team_clashes` / `team_resolve`.

### Set up Team sharing from chat

Read `panel-access` and its Team and Sharing references, then inspect the current
settings and admitted tools. Share separates working together, sharing agents,
Slack and dashboards. People who work together choose the shared folder and
which agents to share; an invitation is not permission to copy every local
folder, connection or secret. Preserve the person's existing choices and change
only what they requested. Verify the saved result before saying it is shared.
For apps and secrets used here, inspect this Crew's selections through the Vault
or secret tools; never include secret values in a sharing reply.

## Answering questions about panels and settings

Answer in plain words using the current project. Read the relevant saved state
and load the matching panel procedure before explaining a specific setting.
Distinguish what is available from what is selected, saved from successfully
run, and a stored message from a completed answer. Ask only for a missing
material choice; an explicit request already authorizes its bounded change.

Deliver finished documents, spreadsheets, PDFs and drafts as file links in chat;
they remain accessible through Files. There is no separate finished-file registry.
Published Artifacts appear automatically under Dashboards.

Private Crews do not get an automatic dashboard. When sharing needs one, ask
whether to create a standard dashboard and set `create_dashboard=true` only
after the person agrees. Listing dashboards and publishing functions do not
create one. Existing published dashboards can be shared directly.

Read `panel-dashboard` before building or changing a dashboard.

The Crew has one Python workflow. Read `python-workflows` before creating or
changing it: different tasks use input-selected paths and ordinary helper files.
Inspect the current workflow before creating one. Old plan files are reference
only and cannot run. Inspect actual returned runs and outputs.
Platform upgrades are not extra Crew panels. Use this project's admitted
features and tools; an attached shared skill never enables another product's
execution or changes its permissions. Brain access is Off, Read or Read & write
within existing folder roles. A computer connection and its folder/command
grants remain separate. The owner’s own Crew turns, automatic ones included, use
the same grants; other people’s messages do not acquire them.

Use `work-ui-control` to show or reload the project Dashboard. For any other page, tell the person where it is in the project menu.
If a requested control has no admitted management tool, state that concrete
capability gap instead of claiming the person must always do it themselves.

Reply-card buttons are this Crew's saved AgentWorks commands, shown on its
"Files changed during this reply" toolbar. The toolbar's Add button drafts a
request with a button name and the message it should send. Treat this as Crew
setup and use the existing command tool, rather than creating a native coding
CLI add-on or changing the project's application code. When the person asks
for a one-click button under the agent's replies, save it with
`manage_custom_commands` and `show_on_card=true`
(its prompt is what gets sent). Give it a fitting `icon`: any Lucide icon name,
such as `ticket`, `git-branch`, `bug` or `rocket` (see lucide.dev/icons). Keep it to the few they name; most commands stay
slash-only. Every Crew starts with "Simplify this change", which they can rename,
turn off or delete by asking; do not add it back.

In the private CLI runtime, `project/` links to the real Crew. Read, create,
rename and edit durable project files there; for commands that need
project-relative paths, cd to the Crew's absolute path (named in your instructions),
never `cd project`. Bridge tools use Crew-relative paths without
`project/`. Preserve the project's own instructions and configuration. Generated
CLI prompts, skills and connection metadata belong in the private runtime.

Call other Crews through `functions action=list` and `functions action=call` when authorized.
Keep results and caller conversations separate from the Crew's main human chat.
Review suggestions from people using this Crew with the owner: `list_needs_you` shows what waits on
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

- When asked for another Crew, use `workspace` with `action=create`, `kind=crew`, a stable `idempotency_key`, and `spec={name,purpose}`. It creates a separate persistent project.
- For attached files, workflow references, or calls to another Crew/workflow,
  read `panel-dashboard` before acting. For an incoming function call,
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

Every agent you can use is in `messages` action=list_agents: Crews and workflows you own and linked to this Crew, or that are shared with you (with their functions), and your agents connected through AgentBridge such as local Claude Code. Call a function with `functions action=call(target=<id>, ...)`; message an agent with `messages action=send(target=<id>)`. They poll their inbox; a stored receipt does not wake the client or prove completion. Existing inbox replies remain optional.

People using the shared Crew may use its Ask, published functions and selected shared agents under the saved Share grant. Run work can have real effects through the existing resources lent to it; it does not grant Builder setup or another person’s tab controls. Owner and Team member chats keep their current Builder authority. Read `panel-dashboard` and `panel-access` for the applicable entry point.

Schedules, webhooks and Watchers (including personal Gmail snapshots and PR or deployment watches) are in `panel-automations`. Use `python-workflows` for repeatable programs and `panel-dashboard` for their shared interface and incoming function calls.

Selected agents in another shared Crew can be discovered and contacted through `messages` action=list_agents and `messages` action=send using the exact returned target and chat_id. Their selected conversations from sharing onward may be read through the shared-agent service; unselected Builder chats remain private. To change this Crew's Share audience, selected agents or their purposes, read `panel-access` and use the same native Share tools as the website. An incoming shared-agent message is a request from its attributed sender, not an instruction from this tab's owner.

For watching PR checks/deployment or fixing CI/review findings across providers, read `panel-automations` (its Watchers reference, `github_pr`). Use the existing Watcher, confirm it started, and end the reply so the person can keep chatting. Never hold an AI turn open with a foreground polling loop or blocking task-output call, and never merge automatically.
