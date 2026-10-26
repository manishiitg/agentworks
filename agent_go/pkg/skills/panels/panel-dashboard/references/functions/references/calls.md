# Calls and authoring

Use only actions actually listed in the current chat or MCP connection. Website, Builder and MCP use the same function definitions and existing project checks. Authoring requires the project's Builder; calling does not grant access to its owner's personal account.

1. `functions action=list` with `project` finds its functions and input schemas. In a project's own Builder, listing may omit project.
2. `functions action=call` with `project`, `function` and an `input` object starts real work. Preserve `submission_id` across an uncertain retry. A different intended call needs a different ID. Execution agents automatically scope retries to their run, step and group and poll instead of receiving chat notifications.
3. Save `call_id`. `functions action=status` needs only that ID. Use bounded `wait_seconds` (at most 25) rather than rapid polling. Read status/error, validated result and output files before claiming completion. Access is checked again when reading or replying.
4. A pending question has a request_id. Only the caller may submit `functions action=reply` with call_id, request_id and response. Another project member can read shared results but cannot answer on the caller's behalf.

## Define a function

`functions action=define` is an upsert. Pass `project` and `definition`:

```json
{"name":"check_report","description":"Review this report","action":{"type":"message"},"input":{"messages":["Review the supplied report and explain any problems."]},"input_schema":{"type":"object","properties":{"report":{"type":"string"}},"required":["report"]}}
```

That Crew function uses its named tab by default. `action.tab` selects a shared named function tab; `action.conversation="fresh"` starts a separate chat per call. Earlier calls in a tab are context only. Structured result_schema uses the platform result file and validates output before completion; otherwise the final answer and output files are returned. Complete required background work before ending the function turn. Report progress with the listed progress action.

For a Goal or Flow, action.type is run; action.step may bind a saved Goal step. Fixed input has groups/routes/payload where supported. The input schema's properties must name declared, non-secret variables; unknown, missing or mistyped input is refused before execution. A Flow function needs required object INPUT and uses its published contract and execution. Current live sharing and allowed_callers still narrow that published version. A Goal returns its run outcome.

`functions action=delete` takes project and name. Change an imported published agent's function at its source, then publish it; do not copy or replace that source from the receiving project.

## Existing saved step bindings

A saved linked-product step may name an explicit binding by stable ID. Its conversion preserves that ID, caller and input mapping in functions.json. Use its advertised binding/run tool when inspecting an existing step; do not replace it silently. Ordinary function calls do not create synthetic webhook objects or caller-specific bindings. Reading or attaching a project alone is not a request to run it.
