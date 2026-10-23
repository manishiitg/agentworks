---
name: "agent"
description: "Agent steps: the description-only first user turn, optional subsequent items in one shared conversation, optional typed scripted helpers the agent chooses, validation and repair turns, saved-script batches. Load before authoring an agent step."
external: true
external_note: "External mapping: use plan action=list for the current native plan and step schemas. Read get_plan for expected_revision, then pass a stable request_id and the listed native arguments. Internal plan operation names are plan action values, not standalone external tools. Fetch referenced guidance with help action=topic and its listed topic name. Builder action=chat is an optional delegation path when listed; it is not required for native plan edits."
---
**Plan-editing tool arguments:** Before a plan mutation, read `builder-reference/references/plan-editing-tools.md`. Step fields described below belong inside `add_step.step` or `update_step.changes`; route/group/maintenance fields belong inside `parameters`. Use the live type/action-specific schema; these field descriptions do not authorize flat arguments or extra fields.

**Saved-code paths:** Read `workflow.json.code_layout_version` first. In this reference, `<script-dir>` means `code/<step-id>` for version 1, or `learnings/<step-id>` for absent/zero (legacy). Resolve the placeholder before using a path; never infer the version from folders or migrate an existing workflow implicitly. Version 1 executes and repairs canonical source directly, with shared helpers under `WORKFLOW_CODE_ROOT`; only legacy workflows copy code into runs and save it back.

## AGENT STEP

Use `agent` as the canonical agent step: one persistent conversation where later turns need the earlier turns' reasoning, tool output, critique, or context. Design one large sequence per coherent shared-context span. For `prompt_contract="description-first-v1"`, the description executes ALONE as the first user turn; each optional `items[]` entry is a subsequent turn after it finishes. Task-specific policies live in the description's Rules section only. Empty items is valid. Saved agents without this contract keep their legacy charter semantics until reviewed migration; authored_prompt retains explicit prompts.

An Agent can declare `predefined_routes` only for saved scripted Python helpers
with typed inputs and outputs. The agent chooses when to call them. LLM
specialists and persistent multi-agent work belong to an explicit Crew step.
The existing `agent` type stays; there is no new simple-agent type. Automatic
reflection is retired. Knowledge-base writes happen only when the task explicitly asks and the step has write access. Steps read selected skills; Builder and Autopilot maintain them.

Before stamping v1.0.48, the first retained item needs `retained_reason`: a review
note explaining the distinct later phase, the evidence the automatic gate cannot
prove, and the boundary preventing repeated initial work. The note is never sent
as a separate user turn. For public/external effects, a verifier reads receipts
and reports ambiguity; it must not repeat the action to repair confirmation.

The default shape is `[description: complete the whole shared-context span] → [automatic validation, with repair only if checks fail]`. Add optional authored turns before the final gate only when they contribute a genuine next phase or semantic evidence check that automatic validation cannot prove. Never mandate verify/repair turns or repeat execution in item zero. Require run-specific proof/provenance in the output so validation cannot pass a stale or self-asserted success. Do not create separate workflow steps for these checks.

Use multiple large sequences when contexts should not be shared: different credentials/security exposure, independently rerunnable outputs or failure domains, clean-room reviewer independence, human/routing boundaries, or unrelated context that would distract or contaminate the next agent. The builder should decide this from workflow semantics and be able to state the isolation reason.

Supported item types:

- `user_message`: one focused follow-up instruction.
- `foreach`: one templated follow-up per row from a read-only query against `db/db.sqlite`.
- `prevalidation`: a deterministic backend validation gate with corrective feedback sent to the same conversation.
- `scripted`: a finite batch of saved scripted scripts; the runtime executes and validates every call before advancing, without creating agent sessions.

`type: "code"` was removed in workflow contract v1.0.10. Deterministic code lives in a saved `scripted` step definition — the type alone makes it scripted; create it with `add_step` — with its script at `<script-dir>/main.py`. Connect conversational and scripted steps through explicit `context_dependencies`, `context_output`, database contracts, and validation.

Preferred split when deterministic data is needed:

```text
scripted: fetch-and-normalize-authoritative-data
  -> agent: analyze-verify-and-repair-from-fetched-data
```

Batch related API/SDK calls or CLI commands into the fetcher when they share credentials, retry/rate-limit behavior, source, and output contract. The fetcher owns pagination, stable parsing, provenance/freshness, idempotency, fail-closed errors, and deterministic DB/file validation. Do not use one step per endpoint, and do not spend sequence turns reissuing known requests or parsing stable response shapes.

When the request itself needs judgment, use `agent: decide-and-write-request-spec -> scripted: execute-request-spec -> agent: interpret-and-verify-result`.

Do not hide durable computation, side effects, retries, or file handoffs inside conversation state.

## WHEN TO USE IT

Use it when:

- Turns read the same upstream context.
- Critique and correction should happen in the same specialist conversation.
- The unit has one tightly coupled outcome and should fail/retry together.
- The task benefits from choosing declared scripted helpers during execution.

Do not use it when:

- A phase has an independent artifact, independently rerunnable validation/failure domain, model, credential, downstream consumer, or context that should be isolated.
- Work is deterministic code; use a scripted step.
- Work is a fixed API/SDK call, CLI command, data fetch, stable parse/normalize operation, or mechanical write; use a scripted step and consume its durable result here.
- The workflow needs deterministic branching; use `branch` (small in-flow
  decision) or `routing` (major sub-workflow fork).
- Persistent specialist coordination belongs to an explicit Crew step.

## SCRIPTED HELPERS

`predefined_routes` expose saved scripted helpers as named tools. Each helper
owns its typed parameter/result contract, code and validation. Use its named
tool to obtain the result, or the scripted-call lifecycle tools for an async
script. No conversational or ad hoc LLM child agent is available inside a step.
Use an explicit Crew step for that work.

## SCRIPTED BATCHES

Create saved script definitions with `add_step(is_orphan=true, ...)`,
using `insert_after_step_id: ""`. Declare `script_parameters`, explicit dependencies,
and a non-empty `validation_schema`; author and test `<script-dir>/main.py` in
Workshop. An orphan is callable here only if it is a scripted step.
Then reference it from `add_step` / `update_step`:

```json
{
  "id": "collect-evidence",
  "type": "scripted",
  "max_parallel": 2,
  "scripted_steps": [
    {"id": "prices", "step_id": "fetch-prices", "parameters": {"market": "NSE"}},
    {"id": "fundamentals", "step_id": "fetch-fundamentals", "parameters": {"market": "NSE"}}
  ]
}
```

**Parameter contract:** `script_parameters` belongs on the saved scripted script
**definition**; each sequence call supplies its values in `parameters`. The runtime
applies declared defaults and rejects missing required names, unknown names, wrong
types, and values outside an `enum` before launching any script in the batch.
The validated object is supplied to Python as `STEP_PARAMS_JSON`.

**Authored values only:** the sequence agent cannot select or change these call
parameters from earlier conversation results. Parameter values are literal JSON;
there is no conversation/SQL binding or template/environment-variable expansion
inside `parameters`. Do not put a placeholder there and promise it will resolve.
SQL `foreach` templates apply to their own conversational messages only.

Follow this item with a `user_message` to analyze the validated results and report.
A script item can come first: its results are added to runtime context before
a subsequent conversational item. Under description-first-v1 the description has already run; scripts required before the initial task must be upstream prerequisites. If the first follow-up is a scripted batch, its message records why that batch is a distinct subsequent phase; the rationale is not another model turn.

- The runtime checks every reference, parameter contract, and saved source before
  starting the batch. No arbitrary file path, inline code, or agentic step is callable.
- `max_parallel` defaults to sequential (0/1), with a maximum of 8. Use parallel
  execution only for independent scripts, including safe DB/asset writes and no
  conflicting browser-session actions. Calls to the same script serialize.
- Each call gets its own `STEP_OUTPUT_DIR` under the parent Agent step:
  `scripts/items/<item-id>/calls/<call-id>/`. Parameters go through
  `STEP_PARAMS_JSON`. Source is `code/<script-step-id>/main.py`, and the process
  working directory is `code/<script-step-id>/`. The current workflow contract
  requires `code_layout_version: 1`; older workflows must migrate before execution.
- Script definitions own store permissions and output validation. Item-level
  `kind`/`write_access` and message/foreach fields are rejected on scripted items.
- Every call must exit successfully AND pass validation. A failure waits for the
  remaining batch calls and then stops the sequence; subsequent reporting or
  side-effect items do not run. Per-call status and output paths persist in
  `scripts/items/<item-id>/results.json`, with stdout/diagnostics in each call folder.
- Stop cancels active scripts and prevents queued scripts from starting. It does
  not turn partial completion into success. Re-running may repeat side effects;
  scripts must implement their own idempotency/deduplication contract.
- There is no child LLM, automatic code repair, or automatic retry. Fix failed
  source in Workshop and deliberately rerun. Final sequence validation can still
  repair the parent's conversational deliverable; it does not replay script items.
- Remaining limits: calls and parameters are authored in the plan; no SQL-expanded
  script batches, dynamic child selection, agentic children, or cross-run resume.
  SQL `foreach` still sends sequential conversational turns, not script calls.

## CONVERSATION HISTORY

The Agent runs the task and any explicitly authored follow-up phases in one
conversation. Its saved session is an observability record, not durable resume
state. Run and metric history remain available through the backend.

## WRITE ACCESS

Items inherit the step-level DB and knowledgebase permissions, matching a scripted execution step. Usually no item-level access declaration is needed.

Use a non-empty `write_access` object, or `kind`, only when one turn should be narrowed to selected stores:

- database writes: `"write_access": {"db": true}` or `"kind": "db"`
- knowledgebase writes: `"write_access": {"knowledgebase": true}` or `"kind": "knowledgebase"`

An item override can narrow but never exceed the step-level permissions. Write access is folder-level and per-file path lists are rejected. Put required contributions in the task. There is no automatic learning turn after the step.

**Persistent stores and scratch space (the hard allow-list).** Access still depends on the current step/item grants. DB rows use managed tools; file grants do not authorize opening the live database:

- DB rows via `mutate_workflow_db` (never direct SQLite access from an agentic step); `db/assets/` for durable **files** of any format (PDF, image, CSV, JSON, txt, zip). A downloaded or generated file that later steps or the builder must reach goes in `db/assets/` with a reference row in `db.sqlite`. Shared `reports/` and `tool_output_folder/` are also writable; use `db/assets/` for durable result assets.
- `knowledgebase/notes/` — workflow-discovered narrative facts (KB direct-write only). An item with `write_access.knowledgebase` and an attached `access: "write"` knowledgebase_source can also write that source's `notes/` — same notes/-only boundary, same item-level grant, just in a resolved external workflow's KB. Requires the source workflow to have separately granted this workflow write access via its own `kb_write_grants`; see `references/stores.md`'s "Attached knowledge bases" section for the two-sided consent model and its known concurrency limitation.
- `skills/` — reusable HOW-to instructions, readable by steps and maintained by Builder/Autopilot.
- the step's own execution folder + `Downloads/` — volatile per-run scratch (wiped on re-run).

`docs/`, `knowledgebase/context/`, other `knowledgebase/` subfolders, and any custom top-level folder are **builder-only or denied** — do not route a step's durable file there.

## PREVALIDATION

The step-level `validation_schema` is the final gate when the step declares one. The runtime runs it automatically after the configured work turns with no automatic learning/knowledge closing turn. On a normal validation failure, it sends concrete failures back to the same conversation for correction and retries the gate. Infrastructure failures stop the step.

**Validate on what the step actually produces — prefer the db.** Choose the schema by the step's real output, not by habit:

- Produces a structured JSON the next step reads → a `files` rule on that file, and declare the step's `context_output` to the same file name so the prompt and gate agree.
- Produces db state (rows) or a side effect (a record created, a message sent, a lock updated) → a `db` rule: a read-only SQL assertion that the rows/values actually exist in `db/db.sqlite`. Do **not** declare a `context_output` — the runtime will not force a throwaway output file, and the step is checked on the real effect instead of a self-written marker.
- The file **is** the deliverable but the agent writes it → require run-specific **proof** inside it (real ids, values read back from the authoritative system, timestamps the real system produced), so the gate confirms real work, not a self-asserted "done".

A `db` rule is the stronger gate: it checks the source of truth the next step and the report actually read, so a fabricated or stale "success" can't pass by writing a tidy file. A step with **no** `validation_schema` has no gate at all — it is accepted on the agent's word (the weakest possible proof). Give any step whose result matters a real schema, db-first.

**Prevalidation is now the exception, not the default.** The step-level `validation_schema` above is already enforced automatically as the final gate with same-conversation repair — that covers nearly every case on its own. Presume a sequence needs no `prevalidation` item at all. Add one only when you can name a later item in the SAME sequence that must not run unless an intermediate artifact already passed — guarding something costly or hard to undo (a paid tool call, a fan-out, an external side effect), not routine double-checking. If the final configured item already validates the same step-level schema, the runtime does not add a duplicate final gate, and a `prevalidation` item that just restates that schema is pure waste: it burns a repair turn on every run instead of catching anything a later item couldn't have caught by simply failing the final gate.

```json
{
  "id": "verify-output",
  "type": "prevalidation",
  "validation_schema": {
    "files": [
      {"file_name": "output/result.json", "required": true, "validation_type": "json"}
    ]
  }
}
```

For a step that persists to the db (the common case for state/side-effect work), gate on the db instead of a file — no `context_output` needed:

```json
{
  "id": "verify-persisted",
  "type": "prevalidation",
  "validation_schema": {
    "db": [
      {"name": "rows written this run", "sql": "SELECT status FROM processed WHERE run_id = 'iteration-0'", "min_rows": 1}
    ]
  }
}
```

Use deterministic validation for artifacts and schemas. Use a user-message critique turn for subjective review.

For a mutation such as upload, publish, or a DB write, the verification turn should
re-read the system of record and prove the effect. The action's own success message
is not independent evidence. Keep verification and repair in the owning conversation
unless different credentials, an independently rerunnable contract, or a clean-room
review requires separation. Use schemas for structural checks and critique for
semantic judgment; give any deliberate critique loop a clear completion/failure
condition rather than asking for indefinite improvement.

## FOREACH

Use `foreach` when every selected database row must get one conversational turn.
This exists today: `source_sql` reads the workflow's `db/db.sqlite`, not an arbitrary
SQLite file path. Rows are queried and messages expanded once when the item begins,
then processed sequentially in the same conversation. Use `ORDER BY` for a defined
order. This does not create separate plan steps or parallel workers.

Zero rows produce no work turns. A row failure stops the loop. Positive
`max_iterations` caps processing and logs omitted rows; do not claim full coverage
when capped. For "process every task," validate expected task IDs against persisted
results from this run, including missing or failed tasks, before reporting success.

Example:

```json
{
  "id": "review-findings",
  "type": "foreach",
  "source_sql": "SELECT id, summary FROM findings WHERE status='open' ORDER BY id",
  "message": "Review finding {{"{{.id}}"}}: {{"{{.summary}}"}}",
  "max_iterations": 50
}
```

The producer should persist canonical rows with an idempotent write and a documented
table contract. The consumer's SQL must use those actual table/column names and the
intended group/run scope. Verify processed-versus-selected counts and task identities;
a cap, a failed row, or an accidental filter must not look like full completion.

`source_sql` must be read-only. Each result row is bound to `.` in the Go template. The step-level `validation_schema` automatically gates the final aggregate result; add a static prevalidation after the loop only when later items must not run unless that intermediate aggregate passes.

## ROUTE PATTERNS

A saved scripted route is also offered to its Agent step as a named tool (route id `lookup-customer` → tool `lookup_customer(...)`), with its parameters as the tool's input schema; this is how an agent calls a script of the workflow, e.g. a customer lookup. Declare inputs with `script_parameters` (flat typed list) or `script_parameters_schema` (one full JSON Schema for nested or constrained inputs), never both. The script reads them from `STEP_PARAMS_JSON` and returns a value by writing `route_result.json` in `$STEP_OUTPUT_DIR`; the agent receives exactly that JSON. Use these patterns when designing or repairing an agent's `predefined_routes`.

LLM specialist routes and their re-entry conversations are retired. Use an
explicit Crew step when persistent specialist memory or coordination is needed.

Use launch values already supplied by the user; do not ask for the same value again.

## AUTHORING RULES

- Write the stable objective, boundaries, and definition of done in `description`.
- Write the actual ordered execution/verification instructions in `items[]`.
- Make the description a coherent execution instruction for the complete outcome; allow zero items. Keep optional later phases separate from the description turn.
- Require run-specific proof/provenance. Builder adds a semantic verification or repair follow-up only for a concrete gap in automatic validation, not by default. An optional verification-only sequence is valid after the description has performed the work.
- Keep each user-message item focused on one outcome.
- Use explicit durable files or DB rows for cross-step handoff.
- Declare item write access before execution.
- Put the final deterministic acceptance contract in the step-level `validation_schema`; use explicit prevalidation items only for intermediate checks.
- Create another large sequence only when its context should be intentionally isolated, and record the boundary rationale in the plan description/review.
- Keep code and its permissions/validation in saved scripted script definitions; use standalone steps or explicit scripted batch items to execute them.
- Never add a legacy code item. If an old plan contains one, require the v1.0.10 workflow preflight migration.

## Runtime procedures and tools

Read `references/step-description.md` for the runtime skill/tool baseline. Keep task/domain skills selected for their actual use; load runtime procedures on demand rather than copying them into each step.
