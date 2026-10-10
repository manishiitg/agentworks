# Calling Crews and workflows: one model, one set of tools

Status: design, 2026-09-25. Not built. Builds on
[crew-calls.md](../crew-calls.md), which covers functions, `ask`, callers and
timeouts as they exist today.

## Why

A person, a Crew, an agent over MCP, or Slack all want the same two things
from a Crew or a workflow: **ask it something**, or **run one of its
functions with some inputs**. Today each path looks different:

- **Over MCP, Crews and workflows use different tool sets.** Crews have
  `get_crew` and `ask_crew`. Workflows have `get_workflow`, no
  `ask_workflow`, and two ways to ask: `call_workflow_function` with
  `function: "ask"`, and the older `chat` plus `run_status`. The connection's
  instructions describe workflow functions as typed runs only ("never a
  free-text task"), so agents don't offer to ask a workflow at all.
  (Observed 2026-09-25: an MCP client offered `get_crew` / `ask_crew` for
  Crews, but only "plan, files, runs, knowledge" for workflows.)
- **Functions, webhooks and schedules are three separate things.** Each has
  its own screen, its own settings and its own way to call it, although each
  one really means "run this route with these inputs".
- **Running a function takes typing every input.** The owner and repo are
  repeated on every `review_pr` call, and the UI has no Run button.
- **A refused call returns only an error**, not the function's inputs or an
  example of a correct call.

## Principles

1. **A function is the unit.** A function is a route or task plus typed
   inputs. Everything that starts it is a *caller*: a person in the UI, a
   Crew, an MCP connection, Slack, a webhook, a schedule.
2. **Crews and workflows have one surface.** The same verbs apply to both;
   the kind is a detail of the target.
3. **`ask` is the front door; functions are the fast lane.** Anyone can ask
   in plain words and the assistant picks the function and inputs. Callers
   that know the function call it directly.
4. **The server already does most of this.** Crew and workflow calls share
   `startCrewFunctionCall`, the per-caller conversations, activity-based
   timeouts and late answers. The work is mostly surface: tool definitions,
   the UI, instruction text.

---

## Part 1: MCP

### Surface: four core tools plus the gateway

The MCP connection today exposes exactly two tools, `get_api_spec` and
`call_tool`, and every operation goes through them. Most sessions need only
four operations, so those become real MCP tools the client lists and can
approve one by one. Everything else stays behind the gateway.

| Tool | Role |
|---|---|
| `list_agents` | Who is there, what each can do, who is busy |
| `ask` | Free-text question; one conversation per caller per target; `about: call_id` reaches running work |
| `call_function` | Typed run, inputs checked first; a refusal teaches the inputs |
| `get_call` | Progress and result of either, including late and interrupted calls |
| `get_api_spec` / `call_tool` | Everything else: runs, files, plans, knowledge, `chat`, per-function tools |

A token without run permission doesn't see `ask` or `call_function`.
There is no `get_agent`: `list_agents` carries everything needed to call, and
`call_function`'s refusal carries the full input details.

**Agents.** Crews and workflows are both *agents*. The kind is known from the
ID (workflow IDs start with `wf_`; Crew IDs are UUIDs) or from the name.
Every `target` argument accepts a name or an ID. Names match ignoring case
and punctuation. A name matching several agents is refused, with the matches
and their IDs listed.

### `list_agents(query?, kind?, limit?, offset?)`

Kept minimal, around 40–60 tokens per agent:

```json
{
  "agents": [
    {
      "name": "server A-pr-reviweer",
      "id": "wf_fc1adcb0",
      "kind": "workflow",
      "about": "Reviews pull requests on runloop-works/app and posts findings.",
      "functions": ["review_pr(PR_NUMBER: int, GROUP?: staging|prod)"],
      "busy": "review_pr for PR 151"
    },
    {
      "name": "server A Flow Tester",
      "id": "14374cfd-a624-5dd3-91b3-eff300ec5d5c",
      "kind": "crew",
      "about": "Runs live Playwright checks of server A flows and reports with video evidence."
    }
  ],
  "next": "ask(name, message) for anything; call_function(name, function, args) to run one."
}
```

- **Signatures** list only what a caller must or may pass. Inputs with a
  default are left out. `?` marks optional inputs; `a|b` lists the choices.
- **Fields shown only when they apply:**
  - `functions`, when the agent has any;
  - `busy`, while it is running or mid-turn;
  - `can: "read only"`, when this connection can't ask or call it.
- **Never included:** paths, plans, steps, variables, files, prompts.

### `ask(target, message, new_conversation?, about?, wait_seconds?)`

- **Continuing conversation.** One conversation per caller per target,
  continued across asks. `new_conversation: true` starts fresh. The
  conversation shows in the target's Chats as "Asked by <caller>", never in
  its main chat.
- **Workflow targets** are answered by the workflow's Run-mode assistant. It
  answers, can start the right function and report back, and files
  suggestions for the owner. **Crew targets:** the answer is the Crew's final
  reply in that conversation.
- **`about: call_id`** delivers the message into that running call's turn
  (what `ask_function_update` does today). The answer arrives through
  `get_call`. Without `about`, a second ask queues behind the running one, in
  order.
- **Waiting.** `wait_seconds` defaults to 20, max 25.

Responses:
- `{status: "answered", answer, call_id}`
- `{status: "working", call_id, progress, queued_behind, next}`
- `{status: "refused", reason}`: unknown or ambiguous target, no permission,
  empty message. Nothing started.

### `call_function(target, function, args, wait_seconds?)`

Responses use the same statuses:
- `completed`, with `result`, plus `run` for a workflow;
- `working`;
- `refused`, which carries the details the list leaves out:

```json
{
  "status": "refused",
  "problems": ["missing required input PR_NUMBER", "GROUP must be one of: staging, prod (got \"dev\")"],
  "function": {
    "signature": "review_pr(PR_NUMBER: int, GROUP?: staging|prod)",
    "inputs": [
      {"name": "PR_NUMBER", "type": "integer", "required": true, "description": "Pull request number"},
      {"name": "GITHUB_OWNER", "type": "string", "default": "runloop-works"},
      {"name": "GROUP", "type": "string", "enum": ["staging", "prod"]}
    ]
  },
  "try": {"function": "review_pr", "args": {"PR_NUMBER": 149, "GROUP": "staging"}}
}
```

Rules:
- **Defaults** apply before validation.
- **Type conversion** only where it's lossless: `"149"` becomes an integer,
  `"true"` a boolean.
- **Unknown inputs are refused**, never silently dropped: a typo must not
  fall back to a saved value.
- **All problems are reported at once.** An unknown function returns the
  target's signatures.
- **Callers** need run access, plus the function's own `allowed_callers` if
  it has one.
- **Crew calls** run where the owners say (user decision 2026-09-29, issue
  #213 C1). When a Crew calls a Crew with the **same owner**, the turn runs in
  the called Crew's own chat, queued behind anything running there. When the
  owners **differ**, it runs in a conversation of its own for the calling
  person (one per caller and person), never in the owner's chat. Workflow and
  external callers keep their continuing per-caller conversation, the same
  one `ask` uses. **Workflow functions** start a run.
- **No fallback to `ask`**: a typed call runs exactly as given or is
  refused.

### `get_call(call_id, wait_seconds?)`

- **Long-polls:** waits up to `wait_seconds` for completion or new progress.
- **Statuses:** `working`, `answered` / `completed`, `failed` (with `partial`
  when the target produced something), `late` (the answer arrived after the
  caller's wait timed out), `interrupted` (the server restarted; includes
  `last_progress`).
- **Also returns:**
  - the last 5 progress lines, with times;
  - `activity`: what the target is doing right now, from its session;
  - `since`;
  - a `next` line that states the last sign of life.
- **Visibility:** only the caller and the target can read a call. Anyone
  else gets not found.
- **No cancel.** To stop a call, the caller can use
  `ask(target, "stop", about: call_id)`.

### Compatibility

The old tools keep working as aliases through `call_tool`, with the same
handlers and responses: `list_crews`, `list_workflows`, `get_crew`,
`get_workflow`, `ask_crew`, `call_crew_function`, `call_workflow_function`,
`ask_function_update`, `get_crew_function_call` and
`get_workflow_function_call`. They are left out of the instructions. Old names
are removed after one release with no recorded use.

### Connection instructions

> Crews and workflows are both *agents*. `list_agents` shows them. Ask
> anything with `ask`; run a specific function with `call_function`. If you
> get a `call_id`, check it with `get_call` and tell the user the progress.
> Use `get_api_spec` for anything else.

### Legacy CLI

MCP over HTTP is the connection path (see
[agentworks-cli-mcp.md](../getting-started/agentworks-cli-mcp.md)); the
`agentworks` CLI is kept only for existing scripts. It gets **no new
commands**. Its existing `crews` and `functions` groups keep calling the old
tool names, which stay as aliases (see Compatibility above).

### Generated tools per function (in-platform chats)

Crew chats already get one tool per function of each attached Crew or
workflow (for example `server A_pr_reviewer__review_pr`). They keep them. Over
MCP, the same generated names are listed by `get_api_spec` under a
`functions` section. `call_tool("server A_pr_reviewer__review_pr", {...})` then
works directly, with no list-then-call step. MCP still exposes only its two
fixed tools; the generated names are call targets, not new MCP tools.

---

## Part 2: running functions

### The function card

Automation → Functions shows one card per function, for Crews and
workflows. The built-in `ask` is always listed first.

| Section | What it does |
|---|---|
| **Run now** | A form built from the input schema: text fields, number fields, dropdowns for `enum`, checkboxes for booleans, defaults filled in. **Run** starts the call with the viewer as the caller. The result, progress and a link to the run show inline. |
| **Defaults** | A saved default value per input (Part 2.2). |
| **Schedule** | Run on a schedule with fixed inputs. Several schedules per function. |
| **Webhook** | A URL for outside systems, with a mapping from payload fields to inputs (e.g. `pull_request.number` → `PR_NUMBER`) and the existing auth modes. |
| **Slack** | Channels or apps that may start it, e.g. a message such as "review PR 149" in a routed channel. |
| **Callers** | Crews, people and connections allowed to call it (`allowed_callers`), plus each caller's continuing conversation. |
| **Copy call** | The same call as an MCP `call_function`, a curl command against the external API, or a Slack message. |

**Who can use Run now:** people with run access to the workflow, and the
owner and editors of a Crew. Read-only viewers see the card but not the Run
button.

### Default inputs

`WorkflowFunctionInput` and a Crew function's `input_schema` properties get an
optional `default`:

```json
{"name": "GITHUB_OWNER", "type": "string", "required": true, "default": "runloop-works"}
```

- A required input with a default can be left out by any caller. The
  default is applied before validation, so every existing check still holds.
- The Run now form and the refusal message show defaults; `list_agents` signatures leave defaulted inputs out.
- **Per-caller last values:** the UI pre-fills the viewer's last inputs for
  that function, kept in browser storage only. The server does not remember
  per-caller values, so a call never depends on hidden state.

With defaults for owner and repo, `review_pr` needs only `PR_NUMBER`.

### Webhooks and schedules become callers

**Data model:** a function trigger (`kind: "function"`) is the function. A
webhook or schedule that targets it stores:
- `function` (the trigger ID);
- `inputs` (fixed values, for schedules);
- `input_mapping` (payload path to input, for webhooks).

It no longer stores its own route selection and allowed variables. A delivery
goes through the same `dispatchWorkflowFunction` path, so inputs are checked
the same way for every caller.

**Migration:**
- An existing webhook or schedule with a route selection and allowed
  variables keeps working as a *standalone* trigger. The Webhooks view shows
  it as today, with a "Turn into a function" action. That action creates a
  function from its route and variables and re-points the trigger at it.
- Nothing is converted automatically.

**Crews:** schedules on a Crew already deliver a prompt. A Crew function
schedule delivers `call_function` with fixed inputs, into the schedule's own
caller conversation.

### `ask` picks the function

A workflow's assistant already starts runs from free text. Two changes make
that the norm:
1. The assistant's `ask` prompt lists the workflow's functions with inputs
   and defaults. It prefers starting a function over a raw run, so the
   inputs are checked the same way as a direct call.
2. When a required input is missing, the assistant asks for exactly that
   input in one question, instead of guessing or failing.

A Crew's `ask` already sees its functions in its own chat; no change.

---

## Build order

| # | Piece | Size | Notes |
|---|---|---|---|
| 1 | `default` on inputs, applied before validation | S | Server only. Unblocks the short calls. |
| 2 | Helpful refusals: schema, defaults, corrected call | S | Server only; all callers benefit. |
| 3 | MCP: `list_agents`, `ask`, `call_function`, `get_call` as real MCP tools; name resolution; aliases; new instructions | M | Mostly wrappers over existing handlers. |
| 4 | Function card: Run now form, defaults, copy call | M | Frontend plus one "run as viewer" endpoint that reuses `startCrewFunctionCall`. |
| 5 | Workflow `ask` prompt lists functions and asks for missing inputs | S | Prompt text plus a test. |
| 6 | Generated per-function names in `get_api_spec` | S | |
| 7 | Webhooks and schedules as function callers, with "Turn into a function" | L | Data model change and migration UI. Do last. |

Steps 1–3 give MCP users the consistent experience. Step 4 is the
biggest gain for people in the UI.

## Tests

- **MCP (e2e against a local server):**
  - `list_agents` returns both kinds.
  - `ask` by name reaches a Crew and a workflow, and continues the same
    conversation.
  - `call_function` with a missing input returns the schema and a corrected
    call.
  - `get_call` returns progress, then the result.
  - The old names still work.
- **Defaults:** a required input with a default can be omitted; an explicit
  value wins; an invalid default is refused when the function is saved.
- **Run now:** a reader sees no Run button; an editor's run is stamped with
  their caller identity, and the result appears inline.
- **Webhook-to-function migration:** a converted trigger delivers the same
  run as before; an unconverted one is untouched.
- **Agentic check (per the E2E rule for LLM code):** a live MCP client given
  only "ask the PR reviewer about PR 149" picks `ask` with the name and
  relays progress. It signs off in JSON.

## Open questions

1. **Name resolution scope.** Should `ask("latency")` match a Crew and a
   workflow both named "latency", or prefer one kind? Proposed: refuse with
   both matches. Being explicit is cheap.
2. **Run now for Crews.** Crew functions run in the viewer's own caller
   conversation with the Crew. Proposed: yes, the same as MCP. The run shows
   up in the Crew's Chats as "Called by <viewer>".
3. **Should `chat` be removed eventually?** Proposed: keep it; it's the only
   way to run several parallel sessions on one workflow.
