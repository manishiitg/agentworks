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
Mailbox-only registrations must read explicitly; the implemented Claude channel is described below.

#### Delivery into agent CLIs (PLAT-861: proven vs planned)

Receipts distinguish `stored`, `admitted`, `processed` and `typed` (tmux
fallback). An offer is not admission or proof of processing: only the agent's
own acknowledgement admits it. Live push goes only to the attached name; a
mailbox-only name gets stored messages and must read explicitly.

| # | Route | Timing | Reaches an idle session | Works for | Status |
|---|---|---|---|---|---|
| 1 | Native push | Immediate | Yes | Claude Code native session, Codex desktop private IPC | PROVEN live |
| 2 | Native steer during a turn | Immediate, even while busy | Yes | Codex queue, Pi steer | Codex queue implemented; Pi steer planned |
| 3 | Stop hook | When the current turn ends | No | Cursor (app and CLI) `followup_message`, Claude Code `Stop`, Codex `Stop` | PROVEN (fixture proof) |
| 4 | Guarded tmux typing | Immediate when idle | Idle only | Terminal CLIs without a native wake; only through platform live-input routing, only when the exact session is idle with an empty input line. Never for desktop apps | PLANNED (861-64); opt-in, off by default |
| 5 | Resume after the turn | When the current turn ends | Starts a new run | Explicit `/agentworks attach` reconnecting the same session | Implemented |
| 6 | Notification to the person | Immediate, to the person | The person acts | Any client, e.g. an idle Cursor app chat; agent name only, never bodies | Implemented |
| 7 | Mailbox read | When the agent checks | No | Every client; the fallback | PROVEN |

Best route per CLI. Claude Code: native session channel, then stop hook.
Codex: desktop private IPC, then stop hook. Cursor app and CLI: stop hook,
plus a notification when the app is idle; no idle push (865-9). Muse: no
outside push on 1.4.4; the mailbox fallback plus the agent's own built-in
Monitor, which delivered an inbox line into a running session and woke an idle
prompt (observed 2026-10-11; PLAT-866). Pi: mailbox today, with native push and
its installer planned. agy: mailbox today; hooks, native push and installer
planned. Also planned: a Muse monitor command and bare `agentworks attach`
with one installer (861-56, 861-44). Idle sessions without
a native wake get a notification to the person; guarded tmux stays a planned
opt-in. Every CLI keeps the mailbox as the fallback.

How the pieces fit:

- One local bridge process per computer holds a server stream per attachment
  (not yet one multiplexed stream), keeps an outbox with submission IDs while
  the server is down, and catches up from its saved cursor after a reconnect,
  oldest first in batches of 20 for authorized senders in the same account.
  Existing restricted attachments wait for a person to confirm `/agentworks attach`
  before accepting all account senders. Upgrade the server before the CLI. Rationale:
  [the 2026-10-11 bridge decision](DECISIONS.md)
  ("Person-attached plugins use a durable local message bridge").
- The person signs in once. Each attached agent gets its own revocable
  connection created by the CLI.
- Attachment is opt-in per session and person-only. In a Codex or Claude chat,
  `/agentworks attach` connects or reconnects immediately, using a short stable name; optionally choose `/agentworks attach NAME`.
  All same-account senders are accepted; there are no sender allow flags.
  An agent cannot attach itself, and attach text inside a delivered agent
  message is refused. `agentworks attach [NAME]` installs a supported plugin if needed and discovers verified live sessions for person selection.
  `/agentworks detach` revokes the current chat attachment.
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
  person attaches a chat: typing `/agentworks attach <name>` in that
  chat (caught by the CLI's prompt-submit hook, linked to that chat's
  conversation ID, never sent to the model). Bare `agentworks attach` from a separate terminal discovers live sessions for person selection.
  `/agentworks detach`, `agentworks detach` or closing the chat ends it. Attachment is absent from agent MCP tools. This is a tool-surface boundary;
  an agent with shell access to the person's CLI credentials can still exercise
  that credential. It is not OS isolation from that agent. `agentworks agents` lists attached
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

#### Implemented Claude channel

Mailbox reading remains the fallback. Live Claude delivery uses an authenticated
SSE connection from the local stdio bridge: the server pushes when durable messages
change, rather than repeatedly calling the read tool. Attach is opt-in and requires
a purpose, the exact native session ID supplied by that session or its owner, and
an explicit person-controlled attachment. All senders in the same account can
enter the native channel after existing caller and product authorization checks. Moving an address creates a new attachment ID and fences the old
listener and its receipts. The directory reports purpose, provider and whether a
listener is connected; connected does not prove that the native client accepted
a notification. Names and messages remain in the selected workspace across restart.

For Claude Code, the person signs in with `agentworks login` using a **private
connection config for this agent**, then configures a
stdio MCP server named `agentworks-live` with command `agentworks` and arguments:

```json
["--config", "/absolute/path/reviewer.json", "mcp", "serve",
 "--claude-channel", "--agent", "reviewer", "--purpose", "Review incoming work",
 "--native-session-id", "<exact-session-id>"]
```

Start or resume **that exact Claude session** with
`--dangerously-load-development-channels server:agentworks-live`, and approve
Claude's per-session channel confirmation. Channels are in research preview;
Team/Enterprise organizations must enable them. Attachment uses a dedicated CLI API outside the agent tool catalog; agents cannot
attach or move a chat through the messages tool. An ordinary remote MCP connection
alone supplies tools and does not enable push. The bridge declares
`experimental["claude/channel"]` and emits `notifications/claude/channel` through
Claude's native input queue, preserving its handling of tools and typed drafts.
It does not relay tool approvals.

The channel includes `message_id`, sender and `inbox_id`. The receiving agent calls
`agent_message_ack(delivery="admitted")` when it receives the event and
`agent_message_ack(delivery="processed")` after handling it. A successful HTTP send
or notification write alone leaves the receipt `stored`. Explicit
`agent_message_reply` uses the original conversation and attached identity.
Reconnecting uses exponential backoff capped at 30 seconds; acknowledged messages
are not replayed. The bridge persists an offer before emitting it, and suppresses repeated IDs
across reconnect and restart. A crash between persistence and emission is
ambiguous: the message remains explicitly readable; it is not automatically
reoffered as a possible duplicate turn. Deduplicate effects by message ID. Replaced or revoked leases
stop delivery, and current sender/receiver authority is checked before release.

Use native push where supported, with turn-boundary hooks for every installed
CLI. Idle sessions without a native wake notify the person; guarded tmux is
opt-in and off by default. Mailbox reads remain the last fallback. The Claude
channel and Codex queue adapter are implemented as described below. Muse and
guarded tmux integrations remain separate follow-ups. A shared MCP connection still shares mailbox authority: until the
CLI can provision narrow per-agent connections, use independent sign-ins/configs.
See [Claude channels](https://code.claude.com/docs/en/channels) and
[the channel protocol](https://code.claude.com/docs/en/channels-reference).

#### Claude and Codex plugins and the local bridge

The person runs `agentworks attach [NAME]` from a terminal, or `/agentworks attach [NAME]` in the exact coding chat. Attach installs a missing supported plugin, verifies live provider processes and native session routes, registers and attaches together, reuses an existing identity after restart, and provides local setup context asking the agent for purpose with a sanitized online-first roster. A terminal presents verified sessions for selection; in-chat typing is approval without another confirmation. Provider hook trust remains the person's choice.

```text
/agentworks attach
/agentworks attach review
/agentworks detach
/agentworks agents
```

CLI `update` restarts the delivery bridge with the installed binary. `stop` pauses it without deleting identities; `start` restores existing approved attachments. None of these automatically approves a new provider process or broadens folder-sharing authority. Sharing, executor, workflow, Crew and file commands keep their existing semantics. A restarted coding-agent process requires person `attach` again. Exact-session repeat attach validates and reuses its current lease; reconnect retains the guidance cadence and avoids replaying ambiguous offers. Cleanup runs automatically.

AgentWorks sends full messaging guidance once per exact session and refreshes after 24 hours, preserving the timestamp through CLI restarts and lease renewal. Every message retains brief provenance and IDs. Claude's native session receiver independently appends its permission notice on each message; the AgentWorks payload does not control that notice.

For separate agent connections, set `AGENTWORKS_CONFIG` to an independent private
CLI config before launching that provider. Shared configs still share authority.
Bare attach uses a short stable name for the exact session; explicit NAME overrides it. Purpose is optional at attachment. The attached agent
sets one line of up to 80 characters with `agent_message_set_purpose` or
`agentworks bridge message purpose --provider codex --purpose "Review changes"`
(use `claude` for Claude). Updates check the exact lease, cannot rename or attach,
and allow at most one change per minute. Set it once after attachment; update
when the person asks or after 24 hours if the work clearly changed.
All account senders are accepted; sender
allow fields have been removed. Existing attachments created under the old policy
are held offline until the person confirms `/agentworks attach`; their sender
permissions never widen silently. Update the server before installing this CLI. Claude's namespaced command `/agentworks:agentworks` is also accepted. The UserPromptSubmit hook
consumes this person command before the model receives it and supplies its exact
session_id. The Claude channel child is bound to its own ancestor PID plus process
start time; it learns the session ID from this hook registration, never by selecting
the newest chat or by a project/name match. No per-session native ID flag is needed
for the plugin. An agent MCP tool cannot attach/detach. Attach/agents show whether the selected
route is native live delivery or hooks. Optional macOS `terminal-notifier` alerts
link to the exact Codex thread when notification access is enabled; otherwise
a basic alert requires opening the chat manually.

Default UserPromptSubmit and Stop hooks only use the private local socket; an
unattached hook emits nothing and makes no server request. Stop loop guards prevent
unbounded continuation. SessionEnd detaches when the host emits it; some clients
delay that event after switching tabs. Use explicit detach when ending participation.
`agentworks agents` lists local attachments. Terminal `agentworks detach` shows the attached chats for person selection.

The installed plugin starts only a credential-less messaging MCP child for its
exact provider process. Tools use the attached identity for explicit send, peer
directory, bounded manual read, acknowledgement and same-conversation reply.
Unattached tools cannot use the default CLI login. A full AgentWorks MCP connection
is a separate person opt-in. This limits plugin tools; it does not isolate shared
OS credentials from an agent with shell access.

One local bridge process holds current per-attachment server SSE streams, private
session leases and a durable offer journal. This is not yet one multiplexed stream
but does create an independent messaging-only child connection for each attached
agent, bound to its stable agent ID and name. The parent login manages these
connections and revoking it immediately stops authentication and refresh. A child
cannot register another identity, read another agent, or use the general tool
catalog. It supports named-agent messaging; product calls need a separate connection. Before releasing
cached messages it rechecks attachment, current sender and receiver authority and
message retention on the server. Writes and SDK enqueues are offers, not fabricated
admitted receipts. Claude/hook receivers acknowledge admitted and processed;
Codex queue exit zero proves native queue admission, with processed still explicit.
Replies retain the original conversation.

For optional Claude idle wake, `agentworks claude` enables the custom development
channel and Claude requests the person's confirmation. Custom marketplace packaging
alone does not put our plugin on the research-preview allowlist: --channels only
accepts Anthropic-approved or organization-approved plugins. An organization can
approve `{ "marketplace": "agentworks", "plugin": "agentworks" }` using
allowedChannelPlugins; Team/Enterprise also needs channelsEnabled. Default hooks
need no channel launch flag. [Claude channel restrictions](https://code.claude.com/docs/en/channels).

The hook selects an exact Claude session registration or a Codex desktop IPC
route when its verified provider ancestor supports it. It never selects another
chat by recency or name. For a separate Codex native queue, the person supplies `--native-remote unix:///ABSOLUTE/SOCKET`
on the chat attach command or terminal attachment. It must be the private local
Codex daemon that owns the exact session UUID; the adapter never guesses a daemon
or targets a session by name. A normal CLI/desktop chat may be unreachable through
that endpoint. Without native setup, hooks plus a desktop notification apply.
The installer never bypasses Codex hook trust. [Codex plugin packaging](https://developers.openai.com/plugins/build/plugins).

The cache is private (directory/socket 0700/0600), bounded to 2,000 messages per
session and 32 MiB total. Bodies are cleared when offered or acknowledged;
processed metadata is reclaimed under quota pressure, offered metadata after
seven days, through automatic cleanup. Pending bodies are retained
for explicit recovery. The persisted monotonic receive watermark survives cleanup
and restart, so retired IDs cannot create duplicate automatic turns. Replies to
retired IDs use an explicit mailbox read and the returned inbox address. Legacy
direct channels retain only bounded offer metadata and a watermark, never bodies.
SessionEnd retains the identity offline and stops delivery; SessionStart explains
that `/agentworks attach` confirms reconnecting the same session. Explicit detach
removes local state and revokes its child connection. The write-before-delivery crash window is intentionally ambiguous rather
than automatically creating a duplicate turn; recover by explicit mailbox read.
Native failures leave an offer pending for explicit handling and notify the person.
Desktop notifications contain only an agent name, never message bodies. macOS/Linux
local bridge is supported; Windows native bridge support remains a follow-up.

Plugin files: [Claude](../plugins/claude/README.md), [Codex](../plugins/codex/README.md).


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

### Existing desktop and Claude session transports

For an already-open Codex desktop chat, explicitly attach its exact UUID with
`--native-remote desktop+unix:///ABSOLUTE/CODEX_HOME/ipc/ipc.sock` and
`--native-pid PID` of its exact running Codex process. This opt-in
adapter uses the desktop's same-user coordination API, discovers the owner of
that UUID, and requires its untrusted-app-input capability. Peer messages arrive
as `agentworks.agent_message` tool content. A running turn receives them through
the native live path; an explicitly ended turn can start an idle turn. A timeout
or disconnect never triggers a second attempt. Permission settings, models,
composer drafts and existing follow-ups remain under the host's control. This
protocol is version-sensitive and fails closed if the installed app changes.
The same-user IPC trusts processes running under the person's OS account; it is
not isolation from other local processes of that user. Native idle wake starts
under that chat's existing approval/sandbox settings, stated before attach.
Native message batches are capped at 96 KiB; larger batches require an explicit
inbox read, with no automatic retry.

Claude's person-enabled session-messaging feature can receive without restarting
an existing chat: select the exact registration file with
`--native-remote claude+session:///ABSOLUTE/HOME/.claude/sessions/PID.json` and
its matching `--session UUID`. The CLI validates that UUID, current process and
private socket/key; it never selects by session name. This optional provider
transport is version-sensitive. Socket writes remain offers because this version
has no acceptance receipt. The receiver must explicitly acknowledge admission and
processing. The official Claude MCP channel remains available separately.

AgentWorks includes acknowledgement/reply instructions on the first offer for
an attachment and refreshes them on the next offer after 24 hours. The private
bridge persists that timestamp with its offer journal, so a daemon restart does
not repeat guidance. A definite pre-delivery failure restores the prior timestamp
for the next new offer; ambiguous writes retain it. Claude channels use MCP tool
metadata and do not consume this formatting timestamp. Every offer still carries
a short help hint and sender-content label,
sender, message ID, inbox ID and exact attachment metadata. A newly attached
session gets fresh instructions. Claude's own native peer/permission warning is
added by Claude; this cadence only controls AgentWorks text. Sender content cannot
attach or detach a session or grant permissions.

A native receiver whose plugin has not been reloaded can use the narrow fallback
`agentworks bridge message ack --provider claude --message-id ID --delivery admitted`
and then `processed`, or `bridge message reply --provider claude --message-id ID
--message TEXT --submission-id ID`. The same command also offers `send`, `read`
and `peers` for this exact attached session. These commands use the exact provider ancestor
and private local child connection. Codex requires its host `CODEX_THREAD_ID` and the stored provider process key;
a supplied UUID cannot replace a missing host thread identity. Codex messaging
MCP children also require a host-supplied UUID and refuse ambiguous shared-process
selection. Use
`--provider codex`. They cannot create attachments or use the parent tool catalog.

A stable agent reuses its private child connection when moved between sessions
under the same parent. A newly created child supersedes abandoned siblings for
that address/parent. If an existing child is expired or revoked, a person-confirmed
reattach derives a replacement once from the currently approved parent. Resume
requires a fresh provider process key. Offline identities retain their address;
explicit detach revokes their child. Cleanup retires offered metadata, so later
replies need an explicit inbox read and `--inbox` instead of the retired message ID.

Local bridge and credential-less messaging-plugin reads filter historical
messages through the current receiver lease,
same-account sender identity and live peer authorization. Explicit inbox replies require
an inbox containing a currently authorized incoming message. Neither check fabricates
admission. Codex shell replies use CODEX_THREAD_ID as a routing hint together with
the provider process key. A shell of the same OS user can change that variable or
read that user's private credentials; these checks are not OS isolation between
desktop chats. Codex messaging MCP uses host-supplied
`_meta["x-codex-turn-metadata"].thread_id` for each call, so a shared desktop
child routes separate chats independently. A per-session host environment is
the fallback where that metadata is absent. Model tool arguments cannot choose
a UUID; missing or invalid host identity is refused. Install broader shell permissions only under the host's
existing person-controlled trust settings.

A separately approved full AgentWorks MCP/CLI connection retains its own broader
mailbox permissions. Its general `messages` read tool is outside the bridge lease and account
checks; installing messaging does not silently reduce or expand those separate
grants. These checks apply to `bridge message read` and the credential-less
messaging plugin's `agent_message_read`/reply tools.

Terminal attach/detach refuse coding-agent process ancestry even through a PTY; the person uses the in-chat hook instead. A missing plugin install names its provider and current project folder and asks for yes. Explicit NAME cannot take over another live chat: detach that name there first. Offline identities can move through person attach.
