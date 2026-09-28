# Calling a Crew: functions, callers and webhooks

There is one way to call a Crew, and one kind of automation a Crew runs on its
own.

| You want to… | Use | Runs in |
|---|---|---|
| Have another Crew, a workflow, or an MCP/CLI tool use a Crew | **Functions** (`ask` or a typed function) | The caller's own continuing conversation with that Crew |
| Run something on a timer | **Schedule** | The Crew's main chat, or the schedule's own conversation |
| Let an outside system (GitHub, CI) start work | **Webhook** | The Crew's main chat, or the webhook's own conversation |

The Crew's **main chat is for people**. Calls from other Crews, workflows and
tools never land there.

## Functions

Every Crew has the built-in **`ask`**: free text in, and its final reply
comes back as the answer. A Crew can also offer **typed functions**, such
as `run_login_flow(build, env) → {passed, failed_step}`. For these, the
arguments are checked before the call and the result is checked before it is
returned.

- **From a Crew or Builder chat:** use `list_functions(target)`, then
  `call_function(target, function, args)`. Tagging `#crew:<name>` in a message
  also generates a `<crew>__<function>` tool.
- **From MCP or the CLI:** use `ask_crew` / `call_crew_function`, or
  `agentworks crews ask` / `agentworks crews call`.
- **Getting the result:** functions are agentic and usually take minutes, so
  `call_function` returns at once with `status: running` and a `call_id`; a
  calling Crew or workflow gets the result as an automatic notification. Pass
  `wait_seconds` (up to 120) to wait inline for a function you expect to be
  quick. MCP/CLI calls (`call_crew_function`, `ask_crew`,
  `call_workflow_function`) also return at once; clients poll
  `get_crew_function_call` / `get_workflow_function_call`, or pass
  `wait_seconds` (max 25).
- **No duplicate runs:** calling the same function with the same arguments
  while that call is still running returns the running call (`joined`), not a
  new run.
- **While a call runs:** `get_function_call` shows progress without
  interrupting. `ask_function_update` sends a question or extra details into
  the running call.

## Workflow functions

A workflow offers `ask` plus the **functions its Builder exposes**.

- **`ask`** goes to the workflow's assistant (the Run-mode chat, the same one
  MCP `chat` uses). Each caller gets one continuing thread with it, titled
  "Asked by <caller>" in the workflow's chat history. The assistant answers
  questions, and when asked to run something it picks the route, sets the
  variables, waits and reports the outcome. It cannot edit the workflow: a
  requested change or reported problem becomes a suggestion for the owner.
- **Functions** are triggers of kind `function` with a fixed route and
  allowed groups (like a webhook) and typed inputs, such as
  `review_pr(GITHUB_OWNER, GITHUB_REPO, PR_NUMBER)`. Each input is a declared
  workflow variable and is set for that run.

A call with a missing, unknown or mistyped input is **refused before anything
runs**, for example `review_pr: missing required input PR_NUMBER`. It never
falls back to a saved value. The caller gets the run's outcome: its status,
any error, and each step's output (a skipped step says why).

To add one, ask the workflow's Builder, for example "expose the review route
as review_pr taking GITHUB_OWNER, GITHUB_REPO and PR_NUMBER (all required)".
The workflow's **Automation → Functions** tab lists them, with the built-in
`ask`.

**Who may call:** a function has no URL and no secret. The platform identifies
the caller, and nothing in the request can change that:

- a Crew or workflow chat, running for a user with edit access to the
  workflow;
- an MCP/CLI access token with `runs:execute` that includes the workflow.

`function.allowed_callers` can narrow this to named Crews or workflows. Every
run records who called it.

**Webhooks stay strict:** a GitHub webhook on the same route keeps its
signature check, raw payload and key validation. The function checks only its
declared inputs. Both feed the same workflow variables.

- **From MCP/CLI:** `list_workflow_functions`, `call_workflow_function`,
  `get_workflow_function_call`, or `agentworks functions list|call|call-status
  --workflow <id>`.

## One continuing conversation per caller

Each caller gets **one continuing conversation** with each Crew it calls. The
caller is another Crew, a workflow, or an AgentWorks user connecting through
MCP or the CLI. Follow-up calls land in the same conversation, so the Crew
remembers earlier calls from that caller. Calling a Crew with `ask_crew`
repeatedly is effectively a chat with it. Different callers never share a
conversation, and none of them sees the main chat.

The conversation is set up automatically on the first call. The Crew's
**Automation → Functions** tab lists these under **Callers**; **Disconnect**
removes one, and the caller's next call starts a new conversation. Each
caller's conversation appears in the Crew's **Chats** list.

## Long calls, timeouts and restarts

- **The timeout counts from the target's last sign of life.** Signs of life
  are a progress report or any activity in its session. The default is 60
  minutes (`timeout_minutes`). A call that keeps showing activity can run up
  to four timeouts, capped at 24 hours.
- **A timeout doesn't discard the target's work.** When the timeout fires,
  the caller stops waiting and is told so. The target can still report
  progress and return its answer, and `ask_function_update` still reaches
  it. The answer is then sent to the caller's chat as a *late answer*.
- **The same applies to `ask` on a workflow.** A timeout releases the caller
  but the assistant's turn keeps running, and its reply arrives as a late
  answer.
- **A restart interrupts open calls.** Every call is saved under the
  target's `functions/calls/` folder and indexed in `_system/function_calls/`.
  After a restart, `get_function_call` still finds the call. A call that was
  still open when the server restarted is reported as interrupted, with its
  last progress kept.

## Webhooks and schedules

The **Webhooks** tab lists external webhooks only: URL, auth mode (Bearer or
GitHub signature), and where each one runs. **Main chat** puts the run into
the Crew's main conversation. **Own conversation** gives the webhook a
continuing conversation of its own. Schedules offer the same choice.
