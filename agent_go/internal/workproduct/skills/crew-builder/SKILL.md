---
name: crew-builder
description: Read for Crew panel or Ask AI guidance, project setup, reply-card buttons and saved commands, creating another Crew, earlier conversation history, or coding/repository work; follow authorized project boundaries.
---

Use the Crew's existing purpose. Change setup only within the user's
request. Discover current skills, connections, secrets, schedules and functions
before making changes; use their dedicated tools and names, never secret values.
Load the relevant panel-* skill before using its management tools.
For the person's laptop or a folder on their computer, read `crew-laptop`.

Home and the sidebar’s New button open one starting page: ask the person’s
own AgentWorks, choose a use case, resume recent work or create directly.
Studio is the private Code/Cowork shortcut using the existing Crew workspace;
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
| Plan; Files; Database | `panel-plan`, `panel-data`, `panel-data` |
| Memory; Skills | `panel-data`, `panel-data` |
| Chat history; named agents and their owners, models, pins or ordering | `panel-runs`, `crew-agents`, `panel-settings` |
| Access: owners, audience and selected shared agents | `panel-access`, `panel-access` |
| Suggestions from people using this Crew | `panel-access` |
| Functions; Schedules; Watchers; Webhooks | `panel-dashboard`, `panel-automations`, `panel-automations`, `panel-automations` |
| Email inbox or personal Gmail monitoring | `panel-channels`, `panel-channels`, `panel-automations` |
| Vault: apps, saved accounts, secret names and computers used here | `panel-access`; then `panel-access`, `panel-access`, `panel-channels`, `panel-channels` or `panel-settings` as needed |
| Shared Brain; Linked projects & folders | `panel-data`, `panel-access` |
| Browser and saving a reusable browser procedure | `panel-settings` |
| Slack bot; WhatsApp; MCP access for external AI apps | `panel-channels`, `panel-channels`, `panel-access` |
| AgentBridge or Chats from your computer | `panel-access`, `crew-agents` |
| Instructions & identity; Models; Costs & usage; Terminal | `panel-settings`, `panel-settings`, `panel-settings`, `panel-settings` |

## Answering Ask AI and panel questions

Answer in plain words using the current project and named panel. An Ask AI
click prepares a question in the person's current project chat; it does not
send automatically or copy all screen data. Read the relevant saved state and
load the matching attached panel procedure before explaining a specific setting.
Distinguish what is available from what is selected, saved from successfully
run, and a stored message from a completed answer. Ask only for a missing
material choice; an explicit request already authorizes its bounded change.

The project name stays visible beside Home, with Builder and named chats in
one row. Ownership details are on hover; only the tab's owner controls it.
Code's right panel starts with Changes in this chat. Dashboards groups saved
dashboards, Motion dashboards and Artifacts in both Code and Cowork.
Both show Browser, selected Google/Slack accounts, one MCP count linking to the
full MCP list, and configured schedules/watchers/triggers.
All tools (Cmd/Ctrl+K) searches every admitted panel; Full menu below it opens
the grouped Build, Use, Share, Integrations and Setup menu. Back to workspace
returns to the status list. The same panel permissions apply, including Files, Terminal,
Models, Usage & cost, Skills and Access & sharing. Visiting a panel does not
promote it into this status list. Goals and Flows retain their own menus.
Back returns to the project menu or the parent details. Help popups and tours
are removed; explain directly in chat.

Deliver finished documents, spreadsheets, PDFs and drafts as file links in chat;
they remain accessible through Files. There is no separate finished-file registry.
Published Artifacts appear automatically under Dashboards.

Private Crews do not get an automatic dashboard. When sharing needs one, ask
whether to create a standard dashboard and set `create_dashboard=true` only
after the person agrees. Listing dashboards and publishing functions do not
create one. Existing published dashboards can be shared directly.

Live dashboards are managed TSX using shared shadcn/Recharts components, kept
in this project's dashboard list; a quick shareable page (a decision, a report,
a file view) can be HTML. Read `panel-dashboard` before authoring; JSON
dashboards are not supported. Dashboard
Ask/Chat widgets are for shared Run users; Builders use their existing chat.
Static export packages the same saved dashboard and its authorized snapshot,
without live private tools or credentials. Use the current detailed procedure,
not a separate Vite app or a new local server.

Crew Plan is a working artifact, not Goal steps or a Flow graph. Goal-only
Decisions, execution Team, Publish/Notify/Backup automation and
platform upgrades are not extra Crew panels. Use this project's admitted
features and tools; an attached shared skill never enables another product's
execution or changes its permissions. Brain access is Off, Read or Read & write
within existing folder roles. A computer connection and its folder/command
grants remain separate, and apply only to authorized interactive work.

Use `work-ui-control` and the advertised targets when showing a panel helps.
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
slash-only. Every Crew starts with "Check this change", which they can rename,
turn off or delete by asking; do not add it back.

In the private CLI runtime, `project/` links to the real Crew. Read, create,
rename and edit durable project files there; for commands that need
project-relative paths, cd to the Crew's absolute path (named in your instructions),
never `cd project`. Bridge tools use Crew-relative paths without
`project/`. Preserve the project's own instructions and configuration. Generated
CLI prompts, skills and connection metadata belong in the private runtime.

Call other Crews through `functions action=list` and `functions action=call` when authorized.
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

- When asked for another Crew, use `workspace` with `action=create`, `kind=crew`, a stable `idempotency_key`, and `spec={name,purpose,icon}`. The icon defaults to the name's initial. It creates a separate persistent project.
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

Every agent you can use is in `list_agents`: Crews, Goals and Relays you own or that are shared with you (with their functions), and your agents connected through AgentBridge such as local Claude Code. Nothing has to be linked first. Call a function with `functions action=call(target=<id>, ...)`; message an agent with `send_message(target=<id>)`. They poll their inbox; a stored receipt does not wake the client or prove completion. Existing inbox replies remain optional.

People using the shared Crew may use its Ask, published functions and selected shared agents under the saved Share grant. Run work can have real effects through the existing resources lent to it; it does not grant Builder setup or another person’s tab controls. Owner and co-owner chats keep their current Builder authority. Read `panel-dashboard` and `panel-access` for the applicable entry point.

## Watchers

Read `panel-automations` for person-owned callers and personal Gmail snapshots.
Keep Crew plan artifacts in `panel-plan`; execution and incoming function
calls retain their own contract in `panel-dashboard`.

Selected agents in another shared Crew can be discovered and contacted through `list_agents` and `send_message` using the exact returned target and chat_id. Their selected conversations from sharing onward may be read through the shared-agent service; unselected Builder chats remain private. To change this Crew's Share audience, selected agents or their purposes, read `panel-access` and use the same native Share tools as the website. An incoming shared-agent message is a request from its attributed sender, not an instruction from this tab's owner.
