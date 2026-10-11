# Authoring a Crew over MCP

A Crew is a persistent, shareable agent: standing instructions (`purpose`), skills, typed
**functions** other people and agents can call, schedules, and a project folder of files.
Think of it as a shareable MCP with structure: callers use its declared functions; only the owner
edits it.

## Create and edit

- `crew` action=create takes `name`, `role`, `purpose` (the Crew's standing instructions; what it is
  for and how it works) and optionally `icon`, `selected_skills`, `files` (path -> text content),
  `functions`, `schedules`. action=update edits one you own section by section (omitted parts stay):
  `skills` {set, add, remove}, `functions` {upsert, delete}, `schedules` {add, update, remove},
  `files`, `remove_files`. The reply is a compact summary; pass `return_spec: true` for the whole Crew.
  action=export and action=import move a Crew between accounts or servers as a portable spec.
- Only the owner edits. Needs `crews:write`.

## Functions

A function is a typed entry point: `name` (snake_case), `description` (for callers), `instructions`
(what the Crew does when called), `input_schema` and optionally `result_schema` (JSON Schema subset:
type, properties, required, items, enum).

- Each accepted call runs in a fresh, isolated execution with its own output folder
  (`.calls/<call_id>/`), never in the Crew's main chat. A function runs with the OWNER's authority
  for every caller, so write instructions that are safe for whoever may call them.
- Inputs are checked before anything starts. Calls run in parallel up to a per-Crew limit; beyond it
  the caller gets a busy error and nothing is queued.
- With a `result_schema`, the function writes its JSON result to the file named by
  `$FUNCTION_RESULT_FILE` before it finishes. The platform validates it after the run, allows one
  correction turn, then fails the call with the reason. Without a `result_schema` the final message is
  the answer. Other outputs (reports, PDFs, images) go in the call's output folder
  (`$FUNCTION_OUTPUT_DIR`); callers read them by `call_id`.
- Callers: `functions` action=list|call|status|read|reply|calls. `calls` lists recent calls (the owner
  sees all, others their own); the owner also sees the commands each call ran.

## Messages and memory

- Conversational contact is separate from functions: `messages`/`ask_crew` send a message to the Crew,
  it may reply or not, and replies are read from the inbox. The Crew's **Agent messaging** setting
  (`free_text_ask` in settings) turns incoming programmatic messages off; declared functions and people
  chatting in the app keep working.
- Runs have no earlier conversation. Anything a later run must know belongs in `MEMORY.md` at the
  Crew's root or in the Crew's files; say so in `purpose` and in function `instructions`.

## Files

- `files` with `crew_id`: anyone with access reads; the owner writes anywhere except protected paths
  (manifests, `functions.json`, `builder/`, `db/`); other callers who can run the Crew write under
  `shared/<their id>/`. Text in `content`, binary in `content_base64` (up to 11 MiB). Put input files
  there for a function to use, and pass their paths as arguments.

## Schedules and settings

- A schedule sends an instruction (`message` or `messages`) on a cron or cadence, with a timezone; a
  schedule whose name already exists counts as added. Scheduler pauses hold timed runs.
- `settings` (with `crew_id`): models, MCP servers and tools, skills, secrets (owner, write-only),
  browser mode, free_text_ask.

## Checking your work

Call `crew` action=get for the Crew's functions, call a function with `wait_seconds` and read
`status`, then check the result against the schema. A function that finishes without a valid result
is a failed call: read its `answer` and `files`, fix the instructions, and call again with a fresh
`submission_id`.
