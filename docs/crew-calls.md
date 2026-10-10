# Crew messages, functions and triggers

Use a conversation when agents need to talk. Use a declared function when a caller
needs a defined task executed with checked inputs and a tracked terminal outcome.

| You want to… | Use | Receiving context |
|---|---|---|
| Talk to a Crew or workflow agent | Agent messages | Continuing authorized conversation |
| Execute a Crew function | Internal function trigger | Fresh isolated execution per call |
| Execute a workflow function | Internal function trigger | Workflow run with its own step history |
| Run on a timer | Schedule | Its configured conversation |
| Accept an outside-system event | Webhook | Its configured conversation |

## Agent messages

Internal agents send explicit messages using the message tool. Builder/Pulse and
Crew/workflow conversations follow the same rule: the recipient decides whether
and what to send back. The platform does not capture the recipient's final chat
text as an answer. There is no conversational `call_id`, required result or timeout
for silence. A declared function named `ask` remains a function; the built-in
conversational ask is not listed as one.

External callers use MCP `messages` action=send or `agentworks messages send`.
The send receipt returns `inbox_id`, `conversation_id` and `message_id`. Keep the
inbox for that external agent/conversation. Read explicitly sent replies with
`messages` action=read or `agentworks messages read --inbox <id>` using
`after` / `next_cursor`. Optional reads wait up to 25 seconds. `ask_crew` and
workflow `chat` are send aliases with these same semantics.

A Crew's **Agent messaging** switch is on by default. Off refuses all incoming
programmatic conversational messages, including replies in established
conversations. Agents can still use declared functions; human app chat is unchanged.

## Crew functions

Ask the Crew's owner chat to expose a named task with declared inputs and optional
output schema. Invoke it internally with `call_function`, through MCP
`functions action=call`, or with `agentworks crews call`.

- Inputs are checked before dispatch. Missing, unknown or mistyped inputs are
  refused before work starts.
- Every accepted new call starts a fresh isolated execution with its own output
  folder; calls never reuse the main chat or a caller's previous function chat.
- Calls run in parallel by default within the per-Crew limit. When full, the
  platform returns a busy error immediately without queueing.
- Keep `call_id`. Read status, progress, execution messages, final answer/result,
  error and output files. A repeated submission ID recovers its existing call.
- Without an output schema, the platform returns the execution's final message
  and produced files. With a schema, the agent writes JSON to its supplied
  `FUNCTION_RESULT_FILE`; the platform validates it after execution. No
  `return_function_result` tool is required or exposed.
- Reads do not start or interrupt the execution. A waiting human-input question
  can be answered through the matching function reply tool.

For MCP use `functions action=status` and action=read with `call_id` plus a file
name. Legacy `get_crew_function_call` accepts the same status/file-read fields.
For CLI use `agentworks crews call-status --call <id>` or
`agentworks crews call-file --call <id> --file <name>`. Reads are bounded and paged;
text uses `content`, binary uses `content_base64`.

Outputs are private to the caller and Crew owner. Isolation separates chats, but
functions still share authorized project files and `MEMORY.md`; edit them only as
required by the function's purpose and authority. Put per-call results in the
provided output folder.

## Workflow functions

Workflow functions are declared triggers of kind `function`, selecting a route,
allowed groups and typed workflow variables. Ask the workflow Builder to expose
a route with its required inputs. Each call runs the workflow with a new run record
and step history; terminal output and errors come from that run.

MCP uses `functions action=list|call|status|read|reply` with `workflow_id`.
CLI uses `agentworks functions list|call|call-status|call-file --workflow <id>`.
A function has no public URL or secret: authenticated caller access and
`function.allowed_callers` determine who may invoke it. Calling does not expand
execution permissions. Webhooks on the same route retain their own signature/key
checks and raw payload rules.

## History, failures and existing triggers

Call records and outputs are saved. Completed calls remain readable after restart;
interrupted open calls expose their interruption and saved progress. Status reads
show pending input or failure instead of treating the last progress message as
success. When schema output is missing or invalid, one correction turn precedes
validated-final-JSON fallback or failure.

Schedules and public webhooks retain their configured destination policies.
Function isolation does not change those policies. Code does not expose callable
functions; its authorized conversations and outgoing calls remain separate.

See [agent messaging and function contracts](design/agent_messaging.md) for the
full lifecycle and [MCP/CLI setup](getting-started/agentworks-cli-mcp.md).
