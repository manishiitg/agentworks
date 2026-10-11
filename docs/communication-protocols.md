# Communication protocols

This is the starting point for communication between AgentWorks products and
external agents. It describes the implementation on main; availability on a
running server depends on its deployed revision, enabled tools and permissions.

The platform has two communication contracts: **direct messages** for conversation
and **structured functions** for work with checked inputs and a tracked outcome.
MCP, CLI and HTTP are ways to reach those contracts. Workflow steps and Relay
Python calls are execution mechanisms with their own configuration.

## Choose a contract

| Need | Use | Receipt or result |
|---|---|---|
| Talk to a Crew or workflow conversation | Direct message | Delivery receipt; optional explicit replies |
| Execute a named Crew task | Crew function | `call_id`, isolated execution, final answer/result and files |
| Execute another workflow's route | Workflow function | `call_id`, separate workflow run and step evidence |
| Run a published Relay from a product | Relay function/API | Run ID, pinned release, status and JSON result |
| Invoke a fixed Crew trigger in a workflow plan | Native `crew` step | Crew run record and final response validated by the step |
| Receive a provider event or run on a timer | Webhook or schedule | Trigger/run history under that trigger's policy |
| Invoke a connected service tool | Attached MCP tool | That tool's own input, result and permission contract |

An agent's final conversational text is not automatically sent to another agent.
A function's tracked terminal output is returned by the platform. Choose the
contract according to which of those behaviors the caller needs.

## Who can call what

| Caller | Direct messages | Structured functions |
|---|---|---|
| Workflow Builder, Run chat and Pulse | Authorized conversations through the registered message tools | Declared Crew/workflow functions through the shared function tools |
| Crew conversations | Authorized conversations, subject to the receiving Crew's messaging switch | Declared Crew/workflow functions |
| Executing workflow `agent` steps, including delegated agents | Message and wakeup tools are not currently provided by the execution function tool binding | Declared Crew functions, other workflow functions and published Relay functions |
| External MCP/CLI agent | Send/read using its own continuing inbox | Declared functions within its account access, connection scopes and target bounds |
| External application using the Relay API | Relay execution has no Run-chat mode | Published Relay functions via the authenticated runs API |
| Relay Python program or its agents | Only explicitly admitted capabilities; no implicit workflow chat tool set | Explicit Python/SDK or selected MCP calls; no default workflow function tool set |
| Code conversation | Authorized conversations under Code's private access rules | Outgoing calls to permitted shared targets; Code exposes no callable functions |

Tools must be enabled for the product and conversation. A tool's presence does
not grant access to its target. A workflow `scripted` step has no agent tool set
and no dedicated function-call plan configuration today.

## Direct messages

Internal agents use `send_message`, `read_agent_messages` and, when available,
`schedule_message_wakeup`. The server supplies the sender identity and trusted
reply address, stores history and queues delivery into the receiving conversation.
The recipient decides whether to acknowledge, ask a question, send several updates,
send a later answer or stay silent. There is no required result or conversational
`call_id`; a receipt does not prove that work finished.

External MCP uses `messages` with `action=send|read`. CLI uses
`agentworks messages send|read`. A send returns `inbox_id`, `conversation_id` and
`message_id`. Keep that inbox for the external agent's continuing conversation;
different agents sharing an account can use separate inboxes. Continue with the
same `inbox_id`, and reuse `submission_id` after an uncertain send.

Reads are non-destructive and use `after` / `next_cursor`. An optional
`wait_seconds` waits up to 25 seconds. An empty page means no new messages.
External callers poll; ordinary MCP does not guarantee that receipt of a message
starts a new external model turn. Legacy conversational `ask_crew` and workflow
`chat` aliases follow this send/inbox contract.

An internal wakeup prompts another turn in that exact conversation. It does not
resend a message or infer completion. Agents explicitly cancel or reschedule it;
incoming replies do not cancel it automatically. Wakeups use the one-minute
scheduler tick and respect scheduler pauses. A delivery interrupted after starting
is recorded as interrupted rather than blindly sent again.

The receiving Crew's **Agent messaging** switch can refuse programmatic messages,
including replies in existing conversations. It does not disable human app chat
or declared functions.

## Structured functions

The shared internal tools are `list_functions`, `call_function`,
`get_function_call` and `reply_function_call`. External MCP exposes
`functions` with `action=list|call|status|read|reply`; the selected target identifies
the Crew or workflow. CLI exposes corresponding Crew and workflow function
commands. Discover the current definition before constructing arguments.

Inputs are checked before execution. An accepted call returns a `call_id`; retain
it and poll until a terminal outcome. Progress text, a quiet interval and a pending
human-input question do not mean completion. Status exposes saved progress,
execution messages, errors, result and file metadata. File reads are bounded and
paged. Answer a pending input with its matching request ID through the reply tool;
this does not grant authority to approve an action.

A repeated submission ID recovers the accepted call. Use a new ID for intentionally
new work, and reuse it after an uncertain retry. Crew calls have a per-Crew running
limit; when full they return busy without queueing. Call isolation gives each
execution its own conversation and output folder, while authorized project files
and memory remain shared.

Without a Crew output schema, the result includes the execution's final answer and
produced files. With a schema, the agent writes JSON to `FUNCTION_RESULT_FILE`;
the platform validates it. A missing/invalid result gets one correction turn, then
a schema-valid final-JSON fallback or failure. No `return_function_result` tool is
required. Workflow functions instead expose their workflow run's terminal outcome
and step evidence; they do not create an extra reporting chat.

Completed function records remain readable after restart. Open interrupted calls
expose their interruption and saved evidence. Reads do not start or steer an agent.
Knowledge of a call ID alone does not authorize access to its private output.

## Workflow to Crew, workflow and Relay

An executing workflow agent can use the same structured tools for all three
targets. For example, if workflow `Pipeline` declares a `review_pr` function with
these inputs:

```text
list_functions(target="Pipeline")
call_function(target="Pipeline", function="review_pr",
              args={"GITHUB_OWNER":"example", "GITHUB_REPO":"app",
                    "PR_NUMBER":7, "group":"staging"})
get_function_call(call_id="<returned-call-id>")
```

For **Workflow A to Workflow B**, B exposes a trigger of kind `function`: a named
contract selecting a route, permitted groups and typed workflow variables. B's
function caller allow-list must admit A when restricted. Empty allow-lists admit
callers who otherwise have run access. Each accepted new call starts B's own run.
An allow-list admitting A's owner as a user does not automatically admit A as a
workflow caller.

For **Workflow to Crew**, expose a named Crew function and call it with its declared
arguments. The native `crew` plan step is a separate existing path: it binds a
Crew trigger ID, sends the rendered instruction and dependency inputs, waits for
that trigger's final response and applies step validation. It has no named-function
plus typed-arguments plan configuration. Its trigger owns the receiving
conversation policy; it is not the direct-message inbox protocol.

For **Workflow to Relay**, function dispatch uses the published release and checks
live caller permissions. A Relay function invokes its Python entrypoint; branching
belongs in that program rather than workflow route selection.

Execution agents identify the caller as the source workflow, including delegated
agents. The launch account's current access and any MCP connection grants remain
applicable. Default retry identity includes run, step, group, target, function and
arguments. Repeating an identical call in that scope reuses the original call;
another run or group is distinct. Use a distinct explicit `submission_id` for
deliberately repeated work. Status and replies cannot reach another execution
scope. Execution agents poll and cannot request `notify=true`; test mode refuses
cross-project function calls and replies. Adding these runtime tools needs no new
saved-plan shape or workflow contract migration.

## Relay API and execution

`POST /api/relays/{id}/runs` accepts `function`, object `input`,
`idempotency_key` and optional `version`. Omitted version selects the active
published release. The accepted run is pinned to an immutable snapshot; publishing
later does not change it. Poll `GET /api/relays/{id}/runs/{run}` for status and final
JSON, and use `GET /api/relays/{id}/releases` to inspect releases.

Repeating an idempotency key returns its original run, including after a newer
publish. Reusing the key with different input returns a conflict. Frozen source
does not freeze credentials or permissions: current access, connections and
release integrity are checked at admission and platform call boundaries.

New native Python Relays use DBOS `async def run(INPUT)` and durable steps, with
explicit `agentworks` agent/MCP/tool adapters. Existing `run(INPUT, ctx)` programs
and graph Relays remain supported. Recovery depends on the runtime and durability
configuration. DBOS reuses completed checkpoints; uncertain side effects require
reconciliation or an explicitly safe replay policy. A durable Relay does not make
arbitrary external writes exactly once. See the Python contract for recovery
details. Relays have no Run chat, Pulse, cron/calendar schedules or bot chats.

## MCP, identity and other entry points

**Platform MCP** is the external authenticated interface to AgentWorks products:
messages, functions, runs and separately authorized authoring tools. **Attached
MCP servers** are service connections used by admitted agents or Relay code; each
service defines its own tool behavior. Invoking an arbitrary MCP tool does not
automatically create an AgentWorks function record or conversational inbox.

Authorization combines current account/product access, target access, function
caller restrictions and, for token launches, connection scopes, selected target
bounds and revocation checks. External MCP calls identify their signed-in user;
workflow execution calls identify their workflow while retaining launch grants.
Execution continues under the target's authorized role. A function call does not
increase that authority.

Webhooks retain their provider signature/key checks and payload rules. Schedules
retain their destination and timing policies. Bot connectors route provider
messages into their configured conversations. None of these entry points turns
ordinary final chat text into an automatic cross-product reply.

## Detailed contracts and implementation

- [Conversation and function lifecycle](design/agent_messaging.md): canonical lifecycle, inboxes, wakeups, isolation and outputs.
- [Crew messages, functions and triggers](crew-calls.md): practical product calls and CLI commands.
- [MCP and CLI setup](getting-started/agentworks-cli-mcp.md#messages-and-function-calls): external transport and consent.
- [Workflow execution agent calls](workflow/agent_step_design.md#calling-other-projects): scoped identity, retries and runtime tool availability.
- [Workflow API triggers](workflow/api-triggers.md): event authentication and payload mapping.
- [Relay API](relay/README.md#api-reference): runs and releases endpoints.
- [Python Relay contract](design/python_relays.md): current native DBOS and compatibility runtimes, capabilities and recovery.
- [Channel connectors](channel-connectors.md): provider chat entry points.

Implementation starts in `agent_go/cmd/server/agent_messaging.go`,
`external_agent_messages.go`, `crew_functions.go`, `workflow_function_triggers.go`,
`workflow_agent_functions.go` and `crew_step_runner.go`; Relay execution lives in
`agent_go/pkg/relaypython/`. The older `design/functions_and_calling.md` is a
historical proposal, not the current communication contract.

## Human interaction during workflow execution

The five step types are `agent`, `scripted`, `routing`, `branch` and `crew`.
Known values belong in declared launch variables; a consumer can declare
`required_variables: ["MONTH"]` so a missing or blank value stops the selected
run before work starts. Per-step `human_inputs` supplies scoped instructions.
A `branch` with `route_source: "human"` represents a fixed human choice.
Agents use `human_feedback` for immediate interaction such as a live CAPTCHA,
and `create_human_input_request` for a durable request that can wait.
The dedicated `human_input` step is retired. Existing plans need a reviewed
[drained-deploy migration](workflow/human-input-step-retirement.md).
