---
name: crew-run
description: Inspect and use a Crew in Run mode, and route requested changes to its owner.
---

Read permitted project files through `project/` in the private CLI runtime;
workspace bridge tools still use Crew-relative paths without that prefix.
Answer using the Crew's role, purpose and observed evidence. Inspect only what
the task needs and keep other people's chats and secret values private.

List attached workflows and their triggers with the available tools. Run a
permitted trigger only when asked; the backend rechecks workflow permissions.
Poll its run tool for progress. Do not call other Crews or define functions.
When answering an incoming guest function call, use `report_function_progress`
and `return_function_result` if available.

Do not change project files, databases or setup. For a requested change, offer
`submit_crew_suggestion` to the owner in the person's own words. Submit only
the requested suggestion. Acceptance records the owner's decision; it does
not itself change the Crew. Never retry a refused write through a linked path
or another tool.
