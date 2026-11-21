---
name: project-activity
description: Set up the person's Project activity rows (Slack, mail, Drive, tasks) as feed Watchers.
---

# Project activity

Project activity is the set of rows under Git in the project menu. Each row is one feed Watcher. The Watcher checks a source without a model turn and saves what it finds to the person's own feed. The feed is personal, even in a shared or Team Crew: only the person who set it up sees it. Feed Watchers never start work.

When the person asks to set up or change Project activity:

1. List the current Watchers (`automations`, `scope=watcher`, action `list`) and the project's connections. Say in one short list which apps are connected (Slack, Gmail, GitHub, others) and which are not.
2. If nothing useful is connected, say what to connect first and where: Slack and Google in the project's Vault or Connections panel, GitHub with a saved token. Stop there.
3. For each connected app, ask one short question with choices and a skip option:
   - which Slack channels belong to this project
   - which mail matters (a sender, a label or a keyword)
   - which Drive folder or sheet to watch
   - where the tasks live (a GitHub repository's issues, or another tracker)
4. Create one Watcher per answer with `target {kind: "feed", app, link}`:
   - `app` is slack, mail, drive, tasks or other.
   - `link` is an optional https address where opening the row goes (the channel, the folder, the issues page).
   - Use a short `name` the person will recognise, such as "#proj-website" or "Client mail".
   - Use a stable `request_id` per source.
5. Each detector prints events whose payload is `{title, sub, url, at}`:
   - `title` is required and is one line; `sub` is a short second line.
   - `url` is an https link to the item.
   - `at` is the item's own time, in Unix seconds or RFC 3339.
   - Use the item's stable id as the event id, and advance the cursor so old items are not found again. On the very first check, save only the latest few items.
6. Sources:
   - **Slack:** a scheduled detector that reads the chosen channels with the allowed Slack read tools (see the watcher operations reference).
   - **Mail:** `check_mode=inbox` with `email_source=personal_gmail`, filtered in Python on sender, label or keyword.
   - **GitHub issues:** a scheduled detector that calls the GitHub API with the person's selected token secret.
   - **Drive and other trackers:** not available to detectors yet. Say so plainly instead of building a workaround.
7. Run `check` once per new Watcher and confirm its history shows a successful check. Then tell the person the rows are under Git in the project menu.

To change the setup later, update or delete the matching Watcher. Deleting a Watcher removes its row.
