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
For an incoming typed function, follow the execution’s supplied output contract:
report progress with `report_function_progress` when provided, write required
JSON to `FUNCTION_RESULT_FILE`, and explain the outcome in the final response.
The platform records and validates the result.

Run mode may write requested artifacts under `$CREW_OUTPUT_DIR`
(`<crew>/outputs/`; `CREW_RUN_DIR` is its older name). It cannot change other
project files, databases or setup. For a requested change, offer
`submit_crew_suggestion` to the owner in the person's own words. Submit only
the requested suggestion. Acceptance records the owner's decision; it does
not itself change the Crew. Never retry a refused write through a linked path
or another tool.

Registered external agents of your account are discoverable with `list_message_agents` when listed. Use `send_message` with `target="agent:<name>"` to initiate contact. They poll their inbox; a stored receipt does not wake the client or prove completion. Existing inbox replies remain optional.
