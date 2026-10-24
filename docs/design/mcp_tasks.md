# MCP Tasks and account elicitation

The remote MCP endpoint and `agentworks mcp serve` advertise the current
`io.modelcontextprotocol/tasks` extension. A capable client's function call,
function status request or Builder chat/status request can return a durable
task handle. Ordinary clients retain the existing operation responses. Other
tools remain ordinary MCP calls; notably a reusable Run chat session is not
treated as one task, because it can contain several executions.

The small transport adapter only routes Tasks methods. The SDK keeps ordinary
RPC handling and session-based stdio elicitation. Stdio task requests go to
the hosted task service; no job runs in the bridge process. The server stores
random handles with the person, authenticated connection family and existing
job selector in one table. It prunes handles using the existing function
submission retention period. Each get/update/cancel redispatches the native
scoped operation. Completed results are read under current access too.

`tasks/get` returns current state and the original MCP result on completion.
Answerable questions use the shared elicitation adapter and appear in
`inputRequests` only when the client declares form support. `tasks/update`
replies to the exact pending question. Cancellation uses existing controls;
where a function has no cancellation entry point, it reports that the job
continues rather than claiming it stopped. A task handle grants no access.

URL elicitation wraps existing Google, Slack and Vault sign-in operations.
It exposes only an own-origin URL with a short-lived random request ID.
Opening the page requires normal website sign-in, consumes that ID once,
and checks the browser person against the original requester. It never
returns the provider URL to another person. Successful native provider
callbacks mark only the matching OAuth state/account pair complete. Accepting
the browser prompt alone reports pending, not connected. The client can check
the account with its existing status tools after completing sign-in.

Forms use ordinary text or choices, never credentials. URL-only clients do
not receive forms; an empty elicitation declaration retains the protocol's
form shorthand. Source versions and fingerprints carry this guidance into
untouched Brain starter copies while preserving user-owned notes/packages.
