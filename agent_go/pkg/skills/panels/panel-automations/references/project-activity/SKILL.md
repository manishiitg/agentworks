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
6. Sources. Every detector is a scheduled check (every 5 to 15 minutes). It reads through the check's app bridge, which only reads and works the same on a Mac and on a server (see the Watcher tool description):
   - **Slack:** read the chosen channels with the Slack read tools.
   - **Mail and Google Drive / Sheets:** call Google through the bridge's `google/gog` route with gog arguments:
     - mail: `gmail search` with a Gmail query (`from:client@example.com`, `label:Website`, `newer_than:2d`)
     - Drive: `drive ls` / `drive search` on the chosen folder
     - Sheets: `sheets get` on the chosen sheet, reporting the rows that changed
     The person's Google connection for this Crew must allow Gmail reading or that service. If it does not, say which one to turn on in Vault. Read the gog skill for the exact command syntax.
   - **GitHub issues:** call the GitHub API with the person's selected token secret.
   - **Linear, Jira, Notion and other apps:** call the app's own list, get or search tools through the bridge. Only tools the app marks read-only work. If the app has none, say so plainly instead of building a workaround.
   - **Browser backup:** when the app's connection fails (signed out, error, no connection), the detector may read the app's page instead: POST `browser/read` with `{"url": "https://..."}` returns the page's visible text from this Crew's Chrome. It works only with the AgentWorks Chrome extension and only while it is connected; otherwise it returns an error, so keep the connection as the main way in. When this Crew uses several browsers, add `"browser": "<name>"` to read with the one signed in to that app. Pick out the items from the text, give them stable ids (for example the item's link or title), and add `"via": "browser"` to each payload: the row then tells the person to reconnect the app.
7. Run `check` once per new Watcher and confirm its history shows a successful check. Then tell the person the rows are under Git in the project menu.

To change the setup later, update or delete the matching Watcher. Deleting a Watcher removes its row.
