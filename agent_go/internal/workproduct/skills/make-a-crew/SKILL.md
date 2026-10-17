---
name: make-a-crew
description: Explain live Share, or make an independent Crew copy only when explicitly requested.
---

Sharing uses this project's live **Share** setting: Only me, Chosen people or Everyone. Keep the same files, chats, functions, Memory and selected accounts. Do not copy into another product just to share it. Use `share` with the stable workspace ID, or the dashboard's Share button. Local projects stay private in this version.

Use the legacy copy helper only when the person explicitly asks for a separate, independent project. Creation and copying keep the account's existing product/create permissions.

Two ways, both owned by the same person and both using the account's normal rules
(a person who cannot create Crews gets a plain refusal; say so and stop):

1. From this project: call `make_crew_from_this_project` (an optional `name`). It
   copies the project's text files, PROJECT_INSTRUCTIONS.md, skills, MCP connection
   and secret choices (names only, never values) and memory into a new Crew. Chat
   history is never copied and this Code project stays unchanged. Read the reply and
   tell the person in plain words which files came across (`copied_files`) before they
   share the Crew, because a project can hold notes they would not want others to see;
   offer to remove any with `update_crew` (remove_files). `files_not_copied` lists what
   was too large or not text; offer to add those by hand.
2. From scratch: call `create_crew` with a name; role and standing instructions (purpose) are optional, then add files with `write_crew_file` and skills or
   schedules with `update_crew`. `export_crew` and `import_crew` move a Crew as a
   portable spec.

After the Crew exists, help the person finish it in plain steps:
- Functions: ask what others should be able to ask it to do, then add them with
  `update_crew` (functions upsert: a name, what it does, inputs and instructions).
  A function is typed, logged and runs as the person who calls it.
- Test it: call one function or ask the Crew a question and show the answer.
- Share: the person opens the Crew's page and sets Share to Only me, Chosen people or
  Everyone. Say what each means; do not change sharing yourself.
- Give the link from the reply (`link`) so they can open it.

Keep secrets out of files and messages. The Crew uses the person's secret names only.
