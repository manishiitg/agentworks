**Plan-editing tool arguments:** Before a plan mutation, read `builder-reference/references/plan-editing-tools.md`. Step fields described below belong inside `add_step.step` or `update_step.changes`; route/group/maintenance fields belong inside `parameters`. Use the live type/action-specific schema; these field descriptions do not authorize flat arguments or extra fields.

**Saved-code paths:** Read `workflow.json.code_layout_version` first. In this reference, `<script-dir>` means `code/<step-id>` for version 1, or `learnings/<step-id>` for absent/zero (legacy). Resolve the placeholder before using a path; never infer the version from folders or migrate an existing workflow implicitly. Version 1 executes and repairs canonical source directly, with shared helpers under `WORKFLOW_CODE_ROOT`; only legacy workflows copy code into runs and save it back.

## MESSAGE SEQUENCE — THE AGENT STEP

Use `agent` as the canonical agent step: one persistent conversation where later turns need the earlier turns' reasoning, tool output, critique, or context. Design one large sequence per coherent shared-context span. The step `description` is the system-level charter for the whole sequence; every `items[]` entry is a user turn describing how to carry it out.

An agent step may declare `predefined_routes`. When routes exist, the agent gets
bounded sub-agent tools and decides at runtime whether, when, and how often to
call those specialists. Routes define available capabilities; they do not
prescribe execution order. Without routes, the step is a single-agent sequence.
Both forms use the same system prompt and default tool policy; routes add only
the specialist catalog, delegation guidance, and sub-agent lifecycle tools.
The plan type is `agent` for both plain conversations and conversations with specialists. Saved legacy shapes are converted by the v1.0.47 migration.

The default shape is `[complete the whole shared-context span] → [re-open authoritative evidence and prove every criterion] → [repair gaps and double-check]`, followed by the top-level deterministic validation gate. Require run-specific proof/provenance in the output so validation cannot pass a stale or self-asserted success. Do not create separate workflow steps for these checks.

Use multiple large sequences when contexts should not be shared: different credentials/security exposure, independently rerunnable outputs or failure domains, clean-room reviewer independence, human/routing boundaries, or unrelated context that would distract or contaminate the next agent. The builder should decide this from workflow semantics and be able to state the isolation reason.

Supported item types:

- `user_message`: one focused follow-up instruction.
- `foreach`: one templated follow-up per row from a read-only query against `db/db.sqlite`.
- `prevalidation`: a deterministic backend validation gate with corrective feedback sent to the same conversation.
- `scripted`: a finite batch of saved regular scripts; the runtime executes and validates every call before advancing, without creating agent sessions.

`type: "code"` was removed in workflow contract v1.0.10. Deterministic code lives in a saved `regular` step definition — the type alone makes it scripted; create it with `add_step`, or move existing conversational work there with `change_step_type` — with its script at `<script-dir>/main.py`. Connect conversational and scripted steps through explicit `context_dependencies`, `context_output`, database contracts, and validation.

Preferred split when deterministic data is needed:

```text
regular scripted: fetch-and-normalize-authoritative-data
  -> agent: analyze-verify-and-repair-from-fetched-data
```

Batch related API/SDK calls or CLI commands into the fetcher when they share credentials, retry/rate-limit behavior, source, and output contract. The fetcher owns pagination, stable parsing, provenance/freshness, idempotency, fail-closed errors, and deterministic DB/file validation. Do not use one step per endpoint, and do not spend sequence turns reissuing known requests or parsing stable response shapes.

When the request itself needs judgment, use `agent: decide-and-write-request-spec -> regular scripted: execute-request-spec -> agent: interpret-and-verify-result`.

Do not hide durable computation, side effects, retries, or file handoffs inside conversation state.

## WHEN TO USE IT

Use it when:

- Turns read the same upstream context.
- Critique and correction should happen in the same specialist conversation.
- The unit has one tightly coupled outcome and should fail/retry together.
- A routed agent needs a specialist that can be re-entered during the same workflow run.

Do not use it when:

- A phase has an independent artifact, independently rerunnable validation/failure domain, model, credential, downstream consumer, or context that should be isolated.
- Work is deterministic code; use a scripted regular step.
- Work is a fixed API/SDK call, CLI command, data fetch, stable parse/normalize operation, or mechanical write; use a scripted regular step and consume its durable result here.
- The workflow needs deterministic branching; use `branch` (small in-flow
  decision) or `routing` (major sub-workflow fork).
- The agent must choose or revise strategy from evidence; add bounded specialist
  routes when delegation helps. Independent delegation alone does not require a
  separate step type.

## DELEGATION AND CONTROL

The authored `items[]` define durable conversational phases. Optional
`predefined_routes` expose bounded specialist agents, and the agent
agent decides which routes to call from evidence it sees during any turn. It may
skip a route, call several routes, or re-enter a conversational route with new
instructions. A fixed list of workers still belongs in explicit plan steps or a
scripted batch; adding routes does not turn a checklist into adaptive strategy.

The sequence's own conversation remains responsible for reasoning, verification,
repair, and the final result. Scripted items are different: they are predetermined
runtime calls with no child LLM. Running ten known scripts, accounting for every
result, and reporting is sequence work even if those scripts run in parallel.

Read `references/plan-design.md`, Step 2, for the decision rule. Ordered turns,
script batches, delegation lifecycles, and completion gates are enforced by the
runtime, not merely a system-prompt preference. There is no separate mode field:
`predefined_routes` are the capability declaration.

## SCRIPTED BATCHES

Create saved script definitions with `add_step(is_orphan=true, ...)`,
using `insert_after_step_id: ""`. Declare `script_parameters`, explicit dependencies,
and a non-empty `validation_schema`; author and test `<script-dir>/main.py` in
Workshop. An orphan is callable here only if it is a scripted regular step.
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

**Parameter contract:** `script_parameters` belongs on the saved regular script
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
the next conversational item, while the description remains the system charter.

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

## MEMORY

- A top-level agent runs its fixed item queue once.
- A agent specialist route can be re-entered during the same workflow run.
- Route memory is in-memory only. It does not survive process restart or a later workflow run.
- `agent_restart=true` starts a clean route conversation when prior context is stale or contaminated.
- `session.json` is an observability record, not resume state.

## WRITE ACCESS

Items inherit the step-level DB, knowledgebase, and learnings permissions, matching a regular execution step. Usually no item-level access declaration is needed.

Use a non-empty `write_access` object, or `kind`, only when one turn should be narrowed to selected stores:

- database writes: `"write_access": {"db": true}` or `"kind": "db"`
- knowledgebase writes: `"write_access": {"knowledgebase": true}` or `"kind": "knowledgebase"`
- learning writes: `"write_access": {"learnings": true}` or `"kind": "learning"`

An item override can narrow but never exceed the step-level permissions. Write access is folder-level and per-file path lists are rejected. Use direct learning writes sparingly; normal step-level learning runs after the complete step.

**Persistent stores and scratch space (the hard allow-list).** Access still depends on the current step/item grants. DB rows use managed tools; file grants do not authorize opening the live database:

- DB rows via `mutate_workflow_db` (never direct SQLite access from an agentic step); `db/assets/` for durable **files** of any format (PDF, image, CSV, JSON, txt, zip). A downloaded or generated file that later steps or the builder must reach goes in `db/assets/` with a reference row in `db.sqlite`. This is the ONLY step-writable home for an arbitrary file.
- `knowledgebase/notes/` — workflow-discovered narrative facts (KB direct-write only). An item with `write_access.knowledgebase` and an attached `access: "write"` knowledgebase_source can also write that source's `notes/` — same notes/-only boundary, same item-level grant, just in a resolved external workflow's KB. Requires the source workflow to have separately granted this workflow write access via its own `kb_write_grants`; see `references/stores.md`'s "Attached knowledge bases" section for the two-sided consent model and its known concurrency limitation.
- `learnings/_global/` — reusable HOW-to-run knowledge.
- the step's own execution folder + `Downloads/` — volatile per-run scratch (wiped on re-run).

`docs/`, `knowledgebase/context/`, other `knowledgebase/` subfolders, and any custom top-level folder are **builder-only or denied** — do not route a step's durable file there.

## PREVALIDATION

The step-level `validation_schema` is the final gate when the step declares one. The runtime runs it automatically after the configured work turns and before synthetic learning/knowledge closing turns. On a normal validation failure, it sends concrete failures back to the same conversation for correction and retries the gate. Infrastructure failures stop the step.

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

Conversational route sub-agents use `agent`, including stateless one-turn work. Use `regular` only for an explicitly scripted deterministic route.

A saved scripted route is also offered to its Agent step as a named tool (route id `lookup-customer` → tool `lookup_customer(...)`), with its parameters as the tool's input schema; this is how an agent calls a script of the workflow, e.g. a customer lookup. Declare inputs with `script_parameters` (flat typed list) or `script_parameters_schema` (one full JSON Schema for nested or constrained inputs), never both. The script reads them from `STEP_PARAMS_JSON` and returns a value by writing `route_result.json` in `$STEP_OUTPUT_DIR`; the agent receives exactly that JSON. Use these patterns when designing or repairing an agent's `predefined_routes`.

Use an `agent` route when the parent agent should preserve specialist memory. Normal repeated calls reuse the route conversation and each call is delivered as a re-entry user message. Set `agent_restart=true` to restart only when the prior conversation is stale, wrong, or contaminated.

## MESSAGE SEQUENCE ROUTE PATTERNS

- Stateful specialist: re-enter one route for follow-up work.
- Test/fix loop: validate externally, then re-enter the specialist with concrete failures.
- Maker/reviewer: keep creation and independent review in separate routes.
- Panel: separate domain specialists coordinated by the parent sequence agent.
- Clean-room retry: restart a contaminated specialist route.
- Human feedback: send approved operator feedback into the same route conversation.

## AUTHORING RULES

- Write the stable objective, boundaries, and definition of done in `description`.
- Write the actual ordered execution/verification instructions in `items[]`.
- Make the first item a coherent execution instruction for the whole shared-context outcome; do not turn routine phases into separate items.
- Require run-specific proof/provenance, then add a turn that re-opens authoritative evidence and proves every criterion, followed by repair and double-checking.
- Keep each user-message item focused on one outcome.
- Use explicit durable files or DB rows for cross-step handoff.
- Declare item write access before execution.
- Put the final deterministic acceptance contract in the step-level `validation_schema`; use explicit prevalidation items only for intermediate checks.
- Create another large sequence only when its context should be intentionally isolated, and record the boundary rationale in the plan description/review.
- Keep code and its permissions/validation in saved regular script definitions; use standalone steps or explicit scripted batch items to execute them.
- Never add a legacy code item. If an old plan contains one, require the v1.0.10 workflow preflight migration.
