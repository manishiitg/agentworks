---
name: make-a-crew
description: Use when the person wants to share this work with other people, turn it into something others use, or build a new Crew.
---

A Crew is how work from a Code project becomes something other people use: it has
instructions, files, skills, functions that others and other agents can call, and a
page you can share. Offer it when the person talks about sharing this work or making
it usable by others; make it only when they say yes. Never make one unasked.

Two ways, both owned by the same person and both using the account's normal rules
(a person who cannot create Crews gets a plain refusal; say so and stop):

1. From this project: call `make_crew_from_this_project` (an optional `name`). It
   copies the project's text files, PROJECT_INSTRUCTIONS.md, skills, MCP connection
   and secret choices (names only, never values) and memory into a new Crew. Chat
   history is never copied and this Code project stays unchanged. Read the reply:
   `files_not_copied` lists what was too large or not text; offer to add those by hand.
2. From scratch: call `create_crew` with a name, a one-line role and its standing
   instructions (purpose), then add files with `write_crew_file` and skills or
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
