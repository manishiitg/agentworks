---
name: project-activity
description: Set up Project tasks (Watchers and the lead agent), the project's one shared task list kept by a lead agent from Watchers on Slack, mail, Drive, task trackers and GitHub.
---

# Project activity: Project tasks

Project tasks is one shared, curated task list per project, under Git in the project menu. Everyone who can open the project and their agents work from it. Watchers only detect: each new Slack message, mail, ticket or issue goes to the project's **lead agent**. The lead decides what it is:

- **noise**: drop it and record that (`task_sorted kind=dropped`);
- **part of an existing task**: add it there as a source or an activity line (`task_update`), and record `task_sorted kind=added`;
- **new work**: create a task with a short, clear title (`task_add` with its `source`).

Then the lead hands the task to the best-fit agent and keeps it moving through the project's steps. Each task shows where it came from, what is linked to it (tickets, Notion docs, Slack threads, pull requests), what is happening on those, and who moved it.

## Setting it up

When the person asks to set up or change Project activity:

1. List the current Watchers (`automations`, `scope=watcher`, action `list`), the task list (`tasks`) and the project's connections. Say in one short list which apps are connected (Slack, Gmail, GitHub, others) and which are not. If nothing useful is connected, say what to connect first and where (Slack and Google in Vault or Connections, GitHub with a saved token) and stop.
2. Ask, with choices and a skip option: which Slack channels, which mail (a sender, a label or a keyword), which Drive folder or sheet, where tasks live (a GitHub repository, Harmony, Linear, Jira, Notion).
3. Ask for the project's steps (default New → Doing → Review → Done) and any rules in the person's words (what counts as noise, who gets what, when to ask them). Ask whether the lead hands work out by itself (`mode: auto`, the default) or proposes and waits for their approval (`mode: ask`). Save them with `task_settings {steps, rules, lead, mode}`.
4. Set up the lead agent in one pass: `sessions action=open` with `name` (for example "Project lead"), a one-line `purpose` and `instructions` holding the steps below and the person's rules. Reuse an existing lead if there is one. Then save its chat id with `task_settings {lead_chat}` so the person can pause it.
5. Create one Watcher per source with `target {kind: "agent", chat_id: <the lead's chat>, message}`. The message says what the event is and to sort it into Project tasks. Use a short `name` and a stable `request_id`.
6. Run `check` once per new Watcher and confirm its history shows a successful check. Then tell the person the list is under Git in the project menu.

Older feed Watchers (`target.kind = "feed"`) keep their own rows below the list. When the person sets up Project tasks, point each one at the lead instead (update its target), so everything lands in one list.

## Detectors

Every detector is a scheduled check (every 5 to 15 minutes) that prints `{events:[{id, payload:{message}}], state}`. Put a short summary in `message`: source, title, who, link and a short excerpt marked as data. Use the item's stable id as the event id and advance the cursor; on the very first check, record what is already there without sending it.

- **Slack:** the Slack read tools through the check's app bridge.
- **Mail, Drive, Sheets:** Google through the bridge's `google/gog` route (`gmail search`, `drive ls`/`drive search`, `sheets get`). The Google connection must allow that service; if not, say which to turn on in Vault.
- **GitHub issues:** the GitHub API with the person's selected token secret.
- **Linear, Jira, Notion and other apps:** their read-only list, get or search tools through the bridge. If an app has none, say so plainly.
- **Browser backup:** when an app's connection fails, POST `browser/read` with `{"url": "https://..."}` reads the page's visible text in this project's Chrome (AgentWorks extension only, while connected).

## Lead agent instructions

The lead's `instructions` say: for each Watcher event, read `references/project-tasks/SKILL.md` (through the panel-automations skill) and follow its "If you are the lead" part, with the person's rules and steps in their own words. Ask messages the lead sends name the task number and tell the agent to keep the task updated; how agents work a task, add their own and keep the list useful is in that same skill.
