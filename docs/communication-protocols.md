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
| Crew conversations | Authorized conversations, subject to the receiving Crew's messaging switch | Crew Builder offers declared Crew/workflow functions; Run mode has no outgoing function tools |
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

External MCP uses `messages` with `action=send|read|state`. CLI uses
`agentworks messages send|read`. A send returns `inbox_id`, `conversation_id` and
`message_id`. Keep that inbox for the external agent's continuing conversation;
different agents sharing an account can use separate inboxes. Continue with the
same `inbox_id`, and reuse `submission_id` after an uncertain send.

An external inbox belongs to the authenticated **account**, not a device, client
installation or MCP connection. Any client of that account holding its
`inbox_id` can read and reply when its current connection scopes and target
access permit it. Separate inboxes keep conversations distinct; they do not
create a security boundary between clients of the same account.

Reads are non-destructive and use `after` / `next_cursor`. An optional
`wait_seconds` waits up to 25 seconds. An empty page means no new messages.
External callers poll; ordinary MCP does not guarantee that receipt of a message
starts a new external model turn. Legacy conversational `ask_crew` and workflow
`chat` aliases follow this send/inbox contract.

### Named external agents

Claude Code, Codex, Muse and scripts can register an account-owned address with
`messages action=register agent_name=reviewer`. Names are case-insensitive ASCII
letters/digits/dots/underscores/hyphens, start with a letter or digit, and are at
most 64 characters. `messages action=list_agents` lists this account's directory.
Internal Crew and workflow chat agents use `list_message_agents` and can initiate
a conversation with `send_message(target="agent:reviewer", message=...)`.
External senders use `target="agent:reviewer"`; optionally pass their registered
`agent_name` to identify the sending address. Existing project access still applies
when sending to or reading messages from a Crew or workflow.

Registering, removing and sending as a named agent require an existing run-enabled
connection. Registration allows its creating connection by default; the account
can explicitly add live same-account `allowed_connection_ids`. Re-register without
that field preserves the list. Every read/send using a name checks that list and
current consent; a different connection ID needs explicit permission. A browser
session of the account can manage the registrations. Names identify addresses,
not devices: clients sharing an allowed connection can use the same name.

Read with `messages action=read agent_name=reviewer`, omitting `inbox_id`, to receive
first contacts across conversations. Save `next_cursor` and pass it as `after`;
use each message's `reply_address` as `inbox_id` when sending a reply. This mailbox
cursor differs from a single conversation's cursor; do not interchange them.
Reads are non-destructive, limited to 100 messages and bounded to 25 seconds of
waiting. A stored receipt does not mean the client woke, read or completed work.
Until live delivery exists, check at the start and end of work, or explicitly poll with wait.

#### Delivery into agent CLIs (design, PLAT-861)

Today the mailbox only stores messages; an agent sees them when it reads. Live
delivery is planned in this order, most real-time first. A receipt says how far a
message got: `stored`, `delivered` (handed to the CLI) or `typed` (tmux fallback).

| # | Route | Timing | Reaches an idle session | Works for |
|---|---|---|---|---|
| 1 | Native push | Immediate | Yes | Claude Code channels, Codex `codex queue`, Pi extension `sendUserMessage` |
| 2 | Native steer during a turn | Immediate, even while busy | Yes | Pi steer, Codex queue |
| 3 | Stop hook | When the current turn ends | No | Cursor (app and CLI) `followup_message`, Claude Code `Stop`, Codex `Stop`, Muse plugin `Stop` hook (`decision: block`), agy plugin hooks |
| 4 | Guarded tmux typing (optional) | Immediate when idle | Idle only | Terminal CLIs without a native wake (Muse, agy, Cursor CLI); only through platform live-input routing, only when the exact session is idle with an empty input line. Off by default; route 6 is the default for idle sessions |
| 5 | Resume after the turn | When the current turn ends | Starts a new run | Platform-run CLIs (`--resume`, stream-json stdin) |
| 6 | Notification to the person | Immediate, to the person | The person acts | Any client, e.g. an idle Cursor app chat |
| 7 | Mailbox read | When the agent checks | No | Every client; the fallback |

Best route per CLI: Claude Code channel, then stop hook; Codex `codex queue`,
then stop hook; Pi extension; Cursor app stop hook plus a notification when idle;
Cursor CLI and agy stop hook; Muse plugin stop hook, and `muse session-message`
once Muse accepts external session messages. Idle sessions without a native
wake get a notification to the person; guarded tmux is an opt-in fallback. Every CLI keeps the mailbox as the fallback.

How the pieces fit:

- One local `agentworks` daemon per computer holds one stream to the server,
  keeps an outbox with submission IDs while the server is down, and catches up
  from its saved cursor after a reconnect.
- The person signs in once. Each attached agent gets its own revocable
  connection created by the CLI.
- Registration is opt-in per session: `agentworks attach <cli> --name <name>
  --purpose "<what it works on>"`. It installs that CLI's hook or extension
  (merging, never replacing, existing hooks); `agentworks detach` removes it.
  A name is a stable address that moves to the newest attached session and shows
  offline when no session holds it; waiting messages arrive on attach.
- A stop hook asks the local daemon over a local socket (no network call) for
  messages waiting for its conversation and returns them as the next message.
  Several waiting messages go in one follow-up. The hook respects the CLI's loop
  guard (`loop_count`, `stop_hook_active`) and never resends a message.
- Delivered text names the sender, the conversation and how to reply, so the
  agent treats it as a message from another agent, not as the person's
  instruction.
- The person stays in control. Nothing is attached by default, and only a
  person attaches a chat: typing `/agentworks attach <name> "<purpose>"` in that
  chat (caught by the CLI's prompt-submit hook, linked to that chat's
  conversation ID, never sent to the model) or running `agentworks attach`.
  `/agentworks detach`, `agentworks detach` or closing the chat ends it. An agent
  cannot attach its own chat or another one. `agentworks status` lists attached
  chats, and each can be revoked from the Connect page.
- Hooks run for every chat where they are installed, so for an unattached chat
  the hook only asks the local daemon "is this conversation attached?" and exits
  with no output and no network call; if the daemon is not running it exits at
  once. `agentworks attach` installs the hook in the project folder by default
  (`.cursor/hooks.json` and the equivalents), so chats in other projects never
  run it; a global install is the person's explicit choice.
- A stop hook runs only when a turn ends: a long turn delays delivery, and an
  idle session needs route 1, 4 or 6 to wake it.

Native interfaces: [Muse session messaging](https://dev.meta.ai/docs/muse-code/session-messaging),
[Codex app-server](https://developers.openai.com/codex/app-server/),
[Claude channels](https://code.claude.com/docs/en/channels).

CLI uses the same authenticated server, including a local server:

```sh
agentworks messages register --agent reviewer
agentworks messages list
agentworks messages send --agent worker --target agent:reviewer --message 'Ready for review'
agentworks messages read --agent reviewer --after 0 --wait 25
agentworks messages send --agent reviewer --inbox inbox-example --message 'Review received'
agentworks messages unregister --agent reviewer
```

Register `worker` before sending as it. To authorize another connection, use
`messages register --agent reviewer --allow-connection <connection-id>`.
Get the connection's ID from `help action=context` / `agentworks guidance context`;
it is public connection metadata, not a bearer token. Addresses route only within
this server and account. Agents on different machines can use the same server;
delivery between separate AgentWorks servers remains a separate follow-up.
There are at most 100 registrations per account and 2,000 per server. Names
persist until explicitly unregistered; message retention/capacity uses the
existing conversation store. Recreating a removed name gives it a new identity;
old inboxes cannot redirect messages to the replacement. No workflow migration
is required.

An internal wakeup prompts another turn in that exact conversation. It does not
resend a message or infer completion. Agents explicitly cancel or reschedule it;
incoming replies do not cancel it automatically. Wakeups use the one-minute
scheduler tick and respect scheduler pauses. A delivery interrupted after starting
is recorded as interrupted rather than blindly sent again.

The receiving Crew's **Agent messaging** switch can refuse programmatic messages,
including replies in existing conversations. It does not disable human app chat
or declared functions.

Owners can inspect a selected Crew or workflow with MCP `messages action=state`.
The paginated view includes inbox and receiving session IDs, the last 20 delivery
records per inbox, errors and wakeup status; it excludes message bodies. Current
owner authority and connection target bounds are checked before and after the
read. Shared Crew callers and workflow readers cannot inspect other callers.
Queued external messages retain only their sending connection ID; revoked or
expired connections cannot start a delivery after queueing. Delivery also
rechecks participant accounts and current workflow/Crew identity and access.

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

For Crew authors, ordinary shared artifacts belong in `CREW_OUTPUT_DIR`
(`<crew>/outputs/`; `CREW_RUN_DIR` remains its older name) for every caller.
Structured calls have their own `FUNCTION_OUTPUT_DIR` and result contract; use
those supplied paths for per-call output, rather than branching on caller identity.
Typed Crew functions run with the owner’s authorized role; conversational Run
turns retain their restricted setup permissions and permitted output writes.

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

MCP clients that advertise elicitation can render a function's pending
`human_feedback` question while polling its call status. The hosted endpoint
uses capability-gated `input_required` retries; the stdio bridge also supports
the legacy form exchange. Answers use the same authenticated call-scoped reply
path. Elicitation does not push a question to a client that stopped polling;
clients without it use `pending_inputs` and explicit reply tools. Credential and
one-time-code questions stay on poll/reply. Conversational `ask` aliases use
message inboxes and do not produce function-call elicitation. See
[MCP elicitation](design/mcp_elicitation.md).

Webhooks retain their provider signature/key checks and payload rules. Schedules
retain their destination and timing policies. Bot connectors route provider
messages into their configured conversations. None of these entry points turns
ordinary final chat text into an automatic cross-product reply.

## Limits and retention

| Surface | Current limit or retention |
|---|---|
| Direct messages | 1–20,000 Unicode characters per message; 40 accepted messages per hour per conversation, counting both directions. Identical retained submission retries reuse their receipt. |
| Inbox history | At most 1,000 messages; old delivered/stored messages may be removed. A pending or dispatching oldest message prevents eviction and can make the inbox full. Cursors do not make history permanent. |
| Inbox lifetime | Cleanup on new conversation admission removes idle inboxes after seven days of no message activity. At 300 inboxes for one initiating account, or 2,000 across the store, it evicts the oldest idle inbox. Pending/dispatching messages protect their inbox. |
| Expired inbox lookup | Bounded endpoint metadata, without message bodies, lasts up to seven more days (at most 2,000 entries). An authorized lookup returns `expired`; older or unknown IDs and unauthorized callers retain the existing unavailable response. Start a new conversation. |
| External waits and message pages | MCP message/function waits cap at 25 seconds; direct-message pages cap at 100 messages. Internal function fast waits have a separate 120-second cap. |
| Crew function concurrency | Three isolated executions per Crew. A full Crew returns busy without queueing; an accepted identical retry reuses its call. A timed-out execution retains its slot while still running. |
| Function retry IDs | Saved explicit `submission_id` bindings last seven days. In-memory records and old records without a timestamp can retain a binding longer. Use a fresh ID for new work; do not depend on a recurring ID automatically becoming fresh at a particular time. |
| Result JSON and file pages | Result JSON validation caps at 16 MiB. Results up to 256 KiB may be inline; larger results use a file reference. File pages cap at 2 MiB. |
| Crew call outputs | Cleanup removes `.calls/<call_id>/` folders seven days after their last directory change, swept at most hourly per Crew on later function activity. Calls still held as running are protected. The durable call record remains; reading a previously recorded output that has been removed returns `expired`. Save required output before cleanup. |
| Workflow and Relay artifacts | Workflow function artifacts use their run folder and its retention policy; the Crew `.calls` rule is not a promise about those folders. Published Relay sources are pinned releases, while run-output retention follows the Relay/runtime policy. |

These bounds are admission and storage controls, not completion signals. An
empty page or expired output does not start another call or authorize replay of
external effects. A retained call record can outlive both its retry-ID binding
and its output files.

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
