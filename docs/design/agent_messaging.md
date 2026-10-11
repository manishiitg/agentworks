# Agent conversations and function executions

AgentWorks has two separate contracts. Conversations are explicit messages between
agents. Functions are isolated executions of declared internal triggers.

| | Conversation | Function |
|---|---|---|
| Start | Send a message | Invoke a named trigger with checked inputs |
| Receiving context | Continuing authorized chat | Fresh isolated execution/chat |
| Response | Recipient explicitly sends any messages it chooses | Platform records execution's terminal output |
| Silence | Valid; no required reply | Execution still has a tracked status |
| Caller reference | Conversation/inbox address and message cursor | `call_id` |
| Follow-up | Agent decides whether and when to send or request a wakeup | Poll execution history/status or read its files |

## Agents own the conversation

An agent's normal final chat text is not forwarded automatically. It sends each
message explicitly using the message tool and the trusted recipient/reply address.
The recipient can acknowledge, ask a question, send multiple updates, send a later
result or stay silent. Ending either agent's turn does not finish a request.

Go authenticates the sender, enforces access, stores history and schedules delivery
into the recipient's turn queue. Message IDs support retry deduplication; they do
not impose a request/result lifecycle. Delivery never marks Goal Work complete.
Builder and Pulse keep their own roles and current authority; Pulse evaluates
received evidence using its goal tools.

An agent may explicitly request a durable wakeup to check again later. The timer
prompts another turn; it does not resend the question. Received messages do not
cancel reminders automatically. An agent explicitly cancels or reschedules them.
Simultaneous timer and message deliveries are serialized for one conversation.
Timers run on the one-minute scheduler tick and respect global/product pauses.
A started delivery interrupted by restart is recorded as interrupted and is not
automatically resent; agents inspect the receiving transcript before deciding to retry.

### External MCP and CLI inboxes

An external send creates or reuses a server-created inbox bound to the authenticated
caller and continuing conversation. Separate agents sharing an account can create
separate inboxes. The acknowledgement contains `accepted`, `message_id`,
`conversation_id` and `inbox_id`; it is a delivery receipt, not an answer.

The receiving agent explicitly sends replies to the trusted inbox address. The
external caller uses `messages` action=read with `inbox_id`, `after`, `limit` and
optional `wait_seconds` (up to 25). It processes the messages then advances its
cursor to `next_cursor`. Reads are non-destructive. An empty page means no new
messages; it does not mean failure or completion. Ordinary MCP cannot be assumed
to start a new external model turn. Polling is the supported baseline.

Use `messages` action=send with a Crew/workflow target and `message` to start or
continue. Reuse the returned `inbox_id` on continuation and the same
`submission_id` after an uncertain delivery. `ask_crew` and workflow `chat` are
conversational send aliases, without `call_id` or automatic final-answer capture.

### Crew messaging switch

The Crew's **Agent messaging** setting remains stored as `capabilities.free_text_ask`.
It is enabled unless explicitly false. Disabled means all incoming programmatic
conversational messages are refused, including replies and messages in existing
conversations. Agents can still invoke declared functions under their permissions.
Human app chat remains available. This switch does not change execution authority.

## Functions are internal triggers

Every accepted new function invocation has its own isolated conversation and output
folder, including calls by a target's owner. It uses the target's existing project,
resources, memory, tools and authorized execution role. Chat isolation does not
copy the project or isolate its shared files. Code remains private and does not
expose structured functions.

Calls run in parallel by default. A per-Crew running limit bounds resource use;
reaching it returns a busy error without a queue. The caller decides whether to
retry. A duplicate submission recovers the existing accepted call instead of
starting duplicate work.

The platform observes the terminal execution outcome. Intermediate assistant text,
progress, an idle gap or a pending human-input question does not complete the call.
The final message belongs to this particular isolated execution. A function that
promises completed work must await that work before ending. A function explicitly
defined to start work can return a start receipt, which completes only that trigger.

### Output contract

Without a declared output schema, the call returns its final `answer` and produced
files. With an output schema, the execution writes JSON to the supplied
`FUNCTION_RESULT_FILE` in its private `.calls/<call_id>/` output folder. Go reads and
validates it after the execution ends, while preserving the final free-text answer.
The agent does not call `return_function_result`.

A missing/invalid declared result receives one correction turn in the same
execution. If still invalid, the final message may be used only when it parses as
valid JSON for that schema; otherwise the call fails with the final text retained.
Actual execution failure/interruption remains a failure, not a formatting retry.

Poll `functions` action=status with `call_id` for status, progress, bounded execution
messages, answer/result, errors and `files` metadata. Use `after` and `message_limit`
for message pages; start `after` at -1, then use `next_after`. For bounded conversation text, use `after_event` (initial -1) and `next_after_event` to page `events`; raw tool arguments/results are excluded. Read a file with action=read, `call_id`, `file`, byte `offset` and
`limit`. The response’s `file` page contains `content` for text or `content_base64`
for binary data. `next_offset` allows a caller to fetch later pages. Output folders are private;
reads require the call's caller or target owner, not just knowledge of a call ID.

Workflow functions retain their workflow run/step evidence and terminal outcome;
no additional model chat is fabricated solely to report that outcome. Stored
completed calls remain readable after restart. Open calls interrupted by restart
expose interruption and saved evidence instead of silently restarting work.

## Transport and execution remain separate

Conversational inbox polling reads messages an agent actually sent. Function
polling reads the platform's execution record and transcript. Neither read starts
or steers the recipient. Human-input reply tools operate inside tracked executions;
they are not substitutes for agent messages. Completion notifications for internal
function callers reference the same saved terminal output, without creating an
extra reviewer or declaring business success from prose.

This contract implements the decisions in PLAT-840 and PLAT-841. Deployment and
live acceptance are recorded in the private tickets.
