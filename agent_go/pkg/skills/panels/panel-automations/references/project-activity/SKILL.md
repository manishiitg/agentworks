---
name: project-activity
description: Set up Project tasks, the project's one shared task list kept by a dedicated lead agent, fed by Watchers or schedules on Slack, mail, Drive, task trackers and GitHub.
---

# Project activity: Project tasks

Project tasks is one shared, curated task list per project, under Git in the project menu. Everyone who can open the project and their agents work from it. The project has one **dedicated lead agent**, its own tab that only sorts and hands out work. Watchers and schedules only bring things in: each new Slack message, mail, ticket or issue goes to the lead. The lead decides what it is:

- **noise**: drop it and record that (`task_sorted kind=dropped`);
- **part of an existing task**: add it there as a source or an activity line (`task_update`), and record `task_sorted kind=added`;
- **new work**: create a task with a short, clear title (`task_add` with its `source`).

Then the lead hands the task to the best-fit agent and keeps it moving through the project's steps. Each task shows where it came from, what is linked to it (tickets, Notion docs, Slack threads, pull requests), what is happening on those, and who moved it.

## Setting it up

When the person asks to set up or change Project activity:

1. List the current Watchers (`automations`, `scope=watcher`, action `list`), the task list (`tasks`) and the project's connections. Say in one short list which apps are connected (Slack, Gmail, GitHub, others) and which are not. If nothing useful is connected, say what to connect first and where (Slack and Google in Vault or Connections, GitHub with a saved token) and stop.
2. Ask, with choices and a skip option: which Slack channels, which mail (a sender, a label or a keyword), which Drive folder or sheet, where tasks live (a GitHub repository, Harmony, Linear, Jira, Notion).
3. Ask for the project's steps (default New → Doing → Review → Done) and any rules in the person's words (what counts as noise, who gets what, when to ask them). Ask whether the lead hands work out (and reassigns stalled work) by itself (`mode: auto`, the default) or proposes and waits for their approval (`mode: ask`). Save them with `task_settings {steps, rules, lead, mode}`.
4. Set up the dedicated lead in one pass: `sessions action=open` with `name` (for example "Project lead"), a one-line `purpose` and `instructions` holding the steps below and the person's rules. Reuse an existing lead tab if there is one. The lead is never the main chat: if the main chat or a working agent has been sorting tasks, say so and offer to move that job to the new tab. Then save its chat id with `task_settings {lead, lead_chat}` so the person can pause it.
5. Bring each source in the cheapest way that works (see "Watcher or schedule" below), always delivering to the lead's chat: a Watcher with `target {kind: "agent", chat_id: <the lead's chat>, message}`, or a schedule with `create_project_schedule` and `chat_id: <the lead's chat>`. The message says where to look or what the event is, and to sort it into Project tasks. Use a short `name` (and a stable `request_id` for Watchers).
6. Add one review schedule to the lead's chat, a few times a working day (for example 10:00, 14:00 and 18:00 on weekdays, in the person's timezone): "Review run: follow 'Review runs' in the project-tasks guide." This is how the lead keeps every task moving (nudging quiet agents, reassigning, moving finished work). Ask how long a task may stay quiet before a nudge (default 24 hours) and save it in `rules`. Skip the schedule only if the person says no.
7. Run `check` once per new Watcher and confirm its history shows a successful check; for schedules, confirm the saved timing. Then tell the person in one short list how each source is watched, and that the list is under Git in the project menu.

Older feed Watchers (`target.kind = "feed"`) keep their own rows below the list. When the person sets up Project tasks, point each one at the lead instead (update its target), so everything lands in one list.

Pause stops all of it: while the lead is paused its Watchers keep their events for later and its schedules skip their timed runs (Run now still runs).

## Watcher or schedule

Pick per source; Watchers are not required.

- **Watcher** (preferred): when a small check script can list new items, such as Slack, mail, Drive, GitHub, or any app with a list or search tool. The check runs without the AI and only wakes the lead when something is new, so it costs almost nothing when nothing happens.
- **Schedule**: when no check can be written, for example an app the lead can only read with its own tools or the browser, or a source the person wants looked at a few times a day rather than as it happens. Pick a gentle cadence (hourly or a few times a day, not every few minutes), since every run is a full lead turn. The message tells the lead where to look and to sort only what is new since its last look.
- Tell the person which one each source uses and why in one plain line ("Slack: checked every 10 minutes, no cost unless something is new"; "Notion: the lead looks twice a day").

## Detectors

Every Watcher detector is a scheduled check (every 5 to 15 minutes) that prints `{events:[{id, payload:{message}}], state}`. Put a short summary in `message`: source, title, who, link and a short excerpt marked as data. Use the item's stable id as the event id and advance the cursor; on the very first check, record what is already there without sending it.

- **Slack:** the Slack read tools through the check's app bridge.
- **Mail, Drive, Sheets:** Google through the bridge's `google/gog` route (`gmail search`, `drive ls`/`drive search`, `sheets get`). The Google connection must allow that service; if not, say which to turn on in Vault.
- **GitHub issues:** the GitHub API with the person's selected token secret.
- **Linear, Jira, Notion and other apps:** their read-only list, get or search tools through the bridge. If an app has none, say so plainly.
- **Browser backup:** when an app's connection fails, POST `browser/read` with `{"url": "https://..."}` reads the page's visible text in this project's Chrome (AgentWorks extension only, while connected).

## Lead agent instructions

The lead's `instructions` say: for each Watcher event or schedule run, read `references/project-tasks/SKILL.md` (through the panel-automations skill) and follow its "If you are the lead" part, with the person's rules and steps in their own words. Ask messages the lead sends name the task number and tell the agent to keep the task updated; how agents work a task, add their own and keep the list useful is in that same skill.
