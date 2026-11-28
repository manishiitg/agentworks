# Owners and users in Access

A project has one main owner (its creator). A project on the server also has:

- **Co-owners**: they build it like the owner and change everything (files, agents, memory, instructions, skills, settings, automations, sharing) and see every chat in it, while turns still run in the owner's server account.
- **Users**: for a project set up for someone to do specific work. A User works in it through chat with a co-owner's working power (commands, files, tools, functions), but sees only their own chats (the project's dashboard opens from its own link), runs on the owner's last-used model, and cannot change its instructions, skills, settings, automations or sharing, nor the extra locked files the owner names. The server refuses those changes on every path (chat, their own AI app over MCP, the website).

A project on a Mac has its Team instead and neither.

**Welcome page.** `WELCOME.md` in the project folder is what people see on an empty project and at the top of each new chat, instead of the plain empty screen. The owner or a co-owner asks the project's chat for it; the agent writes the file (Users cannot change it). Write it in Markdown (title, a few short lines, images or a video from the project folder, e.g. `![How to start](media/how-to.mp4)`), with up to six starter buttons in its front matter; a starter puts its `message` in the message box, ready to finish and send:

```
---
starters:
  - label: Make a promo
    note: From a short brief
    message: "Make a 30-second promo for "
---
# Welcome
```

Make it fit one screen so the starters show without scrolling: a title, one or two short lines, at most one picture or one short video (the screen keeps it to about a third of the height), one short line on how to start, and up to six starters with short labels and notes (they show two to a row). Put longer help in a starter or the project's instructions, not on the page. Show the person the result and read it back after changes. No file means the screen keeps its plain words.

Use `crew_owners` (list; add and remove for co-owners; user_add, user_remove and user_files for users) or the Access panel's Users and Co-owners sections, where the owner picks the person from the server's accounts. Only the main owner adds or removes people; anyone with a seat can list them. Over the tool, give the person's account email or user id; someone who has never signed in cannot be added yet. `user_files` sets a User's extra `locked_paths` (relative to the project); the instructions, skills and settings are always locked. Adding a User who is a co-owner moves them to Users, and the other way round.

Read the list before changing it and read it back afterward. Neither is the dashboard's Share: that shares only its pages, like a Drive file (people added plus general access), never the project. An account administrator is not automatically the owner of someone else's private project.

A workflow is a Python script inside its project. Manage the project's access through the current tools; do not create a separate workflow owner list or conversation.
