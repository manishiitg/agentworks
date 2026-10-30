# Workflow help guide

Use this guide for questions about a Goal panel or an **Ask AI** request.
Read the matching attached `panel-*` skill for the detailed procedure. This
reference explains the product; it grants no tools, access or permission to run work.

## Plain language and current evidence

- Answer the user's question first. Use the panel's visible name and explain
  unfamiliar terms once. People may be doing personal, creative, team or business work.
- Lead with the relevant outcome and what is actually configured or observed.
  Use this project's saved state, records and tools; distinguish missing data
  from zero and a saved setting from successful execution or delivery.
- Keep the answer short. Show paths, IDs, tool names and technical details only
  when requested or needed to explain a concrete problem. Never show secret values.
- Ask a focused question only when a material choice is missing. Do not make
  every explanation end in a question or ask for approval already given.
- Do not edit, configure, approve, or run anything unless the user asks. A clear
  Builder request authorizes that bounded action through the existing tools and
  server checks. Read the saved result before reporting completion.

## Core concepts

- **Goal**: the outcome and success measures, together with the work intended to
  achieve them. A workflow is its saved plan, configuration and execution.
- **Plan**: the blueprint of steps, dependencies and any actual groups or paths.
  Explain the saved plan rather than inventing missing steps or routes. The plan
  and a run's history are different things.
- **Step**: one bounded task in that plan. Use the current plan tools and step
  contract. Retired LLM sub-agent legs and `call_sub_agent`/
  `get_route_description` are not a setup the user needs to restore.
- **Group**: a configured unit of work, such as an account or region. A path or
  branch is a choice that exists in this plan; it is not an additional agent.
- **Run**: real execution of requested work. Read its output, status, logs and
  costs; opening a view or receiving a tool acknowledgment does not prove success.
- **Dashboard**: the project's published visual results and actions. Use current
  data and say what is missing or stale. Live dashboards are hosted React TSX with
  shared shadcn/Recharts components; a quick shareable page (a decision, a report,
  a file view) can be HTML. JSON dashboards are not supported. Keep all views and
  actions when converting.
- **Goals & metrics**: the goal, success criteria, saved metric definitions and
  real readings. Define numbers with Builder; ordinary steps collect them.
  A Crew that looks after your Goals can review the same evidence through its
  available tools. Decisions keep their existing Needs you destination.
- **Knowledge**: Goal-local background notes and structured database records are
  separate from shared **Brain**. Crew **Memory** holds durable project facts;
  **Skills** hold reusable procedures. Do not restore a retired Learnings panel.
- **Build, Share, Use**: build in the website or MCP, share through the existing
  audience, then use the dashboard, selected agents and published entry points.
  Builder and Run access remain the server's decision.

## Choose the relevant panel procedure

Read only the procedures needed for the current question or requested action.
Use attached skills and live schemas, including product-specific differences.

| Panel or task | Read |
|---|---|
| Dashboards, TSX conversion, live data, actions, motion or video | `panel-dashboard` and its relevant authoring/motion reference |
| Plan & progress; starting, stopping or investigating Goal work | `panel-plan`, `panel-runs`, `panel-runs` |
| Goals & metrics | `references/measurement-plan.md`; define and edit with Builder |
| Pending Decisions | `panel-access` |
| Functions; schedules; Watchers; webhooks | `panel-dashboard`, `panel-automations`, `panel-automations`, `panel-automations` |
| Email inbox or personal Gmail monitoring, when offered | `panel-channels`, `panel-channels`, `panel-automations`; respect each product's supported source |
| Files, Database or the Goal's Knowledge base | `panel-data`, `panel-data`, `panel-data` |
| Chat history and named agent tabs | `panel-runs` |
| Vault: apps, saved accounts, secrets and computers used here | `panel-access`; then `panel-access`, `panel-access`, `panel-channels` or `panel-channels` as needed; computer grants use `panel-access` and the admitted `manage_laptop_access` schema |
| Brain, Linked work, or reusable Skills | `panel-data`, `panel-access`, `panel-data` |
| Browser, live interaction or saving a reusable browser procedure | `panel-settings` |
| Access: owners, audience and selected shared Crew agents | `panel-access`; shared agents keep the existing product's supported contract |
| Goal execution Team | `panel-access`; execution assignments are separate from Share |
| AgentBridge or Chats from your computer | `panel-access`; messages and computer connection grants remain separate |
| Slack bot, WhatsApp or connecting an external AI app | `panel-channels`, `panel-channels`, `panel-access` |
| Settings, instructions, Models, Costs and usage | `panel-settings`, `panel-settings`, `panel-settings` |
| Workflow playbooks, Publish, Notify, Backup, Upgrades | `panel-data`, `panel-dashboard`, `panel-channels`, `panel-dashboard`, `panel-settings` |

## Current navigation and assistance

The project name stays beside Home; Builder is a chat tab. Ownership details
are on hover. Goal/Flow menus group Recent, Build, Use, Share, Integrations
and Setup. Studio/Crew uses a status panel: Changes first for Code, Dashboards
for saved dashboards, Motion dashboards and Artifacts, followed by Browser,
selected app accounts and configured automations. All tools (Cmd/Ctrl+K) opens the full admitted tool
inventory, including Access & sharing, Models and Usage & cost. Visiting a
setting does not promote it into Studio’s status panel. Finished files are
linked in chat and remain accessible through Files; there is no separate file
registry. Published Artifacts appear automatically under Dashboards. Workflow playbooks, Publish, Notify and Backup
are in Setup. Access summarizes owners, audience and shared agents; changes
are made through the visible project chat with the existing management tools.

Panel **Ask AI** prepares a question in the current project's chat. It uses the
view/topic and the normal project context; it does not automatically send a
message or include every displayed row. Inspect the relevant current state before
answering about it. Preserve the person's named chat and existing unsent draft.
Panel Back returns to the project menu, or to the parent list for nested details.
Help popups and tours are removed. Give useful guidance directly in the answer.

For UI presentation, read the attached `workflow-ui-control` skill. Inspect
`list_ui_capabilities`, use only advertised views/targets, and require an
`applied` receipt before claiming the switch. A menu item is not proof that its
UI-control target or management tool is available. If a capability is absent,
state the exact limitation and give the current menu destination when useful.

## Product and permission boundaries

- Goals have executable plans, steps and AgentWorks. Crew is an agentic project
  with an artifact plan, Memory and Skills; use `crew-builder` there. Flows
  (Relays internally) use their Python/DBOS graph, code and versioned runs; use
  `relay-builder`, `panel-plan` and `panel-runs`. Do not turn one
  product's plan, run or view contract into another's.
- Run callers use shared entry points. They do not gain Builder setup, another
  person's tab controls, private unshared chats or personal accounts. Selected
  shared agents expose only the conversation covered by the saved Share grant.
- Vault selections and Brain folder roles remain the existing access gates.
  Brain project access is Off, Read or Read & write; it is not folder binding.
  A selected computer is for authorized interactive work, not Goal steps,
  schedules or AgentWorks. Read the relevant skill before changing any grant.
- Publishing to Surge, Vercel or another static host uses the existing Publish
  automation and `dashboard action=export_static` for the same saved dashboard and
  authorized snapshot. It does not create a second app or export live private
  chat/tools/credentials. Inspect its saved visibility and actual publish status.
- Slack account, Slack bot and WhatsApp are distinct channels. Inspect their
  saved connection and supported actions; never substitute a different account
  or send a test message merely to explain setup.
