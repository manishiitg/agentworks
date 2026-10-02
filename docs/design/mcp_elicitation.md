# MCP function-call elicitation

External Crew and workflow functions continue to run after the MCP request that
started them returns. Their blocking questions belong to the function `call_id`
and `request_id`, not the original HTTP connection or a client-supplied chat
session. Callers can always poll `get_crew_function_call` or
`get_workflow_function_call` and answer through the corresponding
`reply_*_function_call` tool.

On the hosted Streamable HTTP endpoint, a client using MCP 2026-07-28 or later
that declares the per-request `elicitation` capability receives an
`input_required` result when a function-call result has a pending question.
The form has a `response` string; exact-choice questions use an enum. The
client retries the same `call_tool` request with `requestState` and
`inputResponses`. AgentWorks submits an accepted answer through the existing
call-scoped reply path, then returns the current function-call status. Decline
or cancel leaves the question pending for another supported answer route.
Questions with obvious credential or one-time-code wording stay on poll/reply;
MCP form elicitation is for non-sensitive input. Question authors must still
avoid asking for secrets through an elicitation-capable call.

Every retry is authenticated and checked against current Crew or workflow
access. `requestState` is a routing hint, not an authorization token. The
server refuses a changed call or workflow ID, an invalid choice, a duplicate
answer, or a request answered by another user. A workflow reader can inspect
pending questions but does not receive an elicitation form it cannot answer.

The hosted MCP endpoint is stateless and constructs a fresh MCP server for
each HTTP request. It cannot deliver the older server-initiated
`elicitation/create` exchange, whose answer arrives in a second POST to the
original session. Legacy clients and modern clients without elicitation keep
the existing `pending_inputs` and reply tools. An early poll with no pending
question returns ordinary status; a later poll can elicit the question.

The local `agentworks` stdio bridge uses the same question form for clients
that advertise elicitation. On older protocol versions, the MCP SDK turns its
`input_required` result into an `elicitation/create` request on the active
stdio session; modern clients use the retry form. Clients without the
capability retain poll/reply. This change does not push an asynchronous
question into a client that has stopped polling. Restart-safe blocking
questions and continuations remain PLAT-370.

Focused tests exercise the hosted MCP wire protocol for capability gating,
Crew and workflow answers, a question arriving after an initial poll, choice
validation, declined input, cross-user denial, and replay refusal. The stdio
bridge has a legacy-client round-trip test. A live ChatGPT, Cowork, or Claude
Code connection has not yet been verified.
