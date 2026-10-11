## Human in the loop for workflows

Use this reference when designing, changing, or operating a workflow that needs
a human answer, approval, review, or correction. Choose the interaction by
what must wait and what should happen after the answer. Preserve the user's
existing choices and authorization; do not ask again for information already
supplied, invent approval, or expand an approval to unrelated work.

This is a map of platform capabilities, not additional tool access. Use only
the tools available in the current session. A step agent that lacks decision,
chat, or execution tools reports the missing input to its caller through its
normal result; it does not manufacture an endpoint, launch another run, or
edit platform decision tables. Workflow design/report authoring belongs to
Workshop; executing a workflow does not authorize changing its plan.

### Choose the interaction

| Situation | Mechanism | What waits / what happens next |
| --- | --- | --- |
| Ordinary clarification, choosing scope, or discussing an output | Normal workflow chat | Ask in the conversation. The user's reply continues the conversation; pass known answers into execution where appropriate. |
| A planned free-form value inside an interactive pipeline | `human_input` plan step (`text`) | Capture a value into a variable; existing legacy choice steps remain supported. |
| A planned fixed-choice decision inside an interactive pipeline | `branch` with `route_source="human"` | The routes are the options. Use an explicit known answer via `route_selections`; otherwise an interactive run prompts, while an unattended run uses a safe non-approval default or fails. |
| An unexpected, urgent human-only input while a workflow step is running | `human_feedback` | The calling execution agent waits for the bounded response card. Answering returns to the same tool call; expiry is not approval. This tool is unavailable in Builder chat. |
| A review decision that can wait beyond this turn/run | `create_human_input_request` | Persist a question in the workflow's decision system and finish/park the affected work. A later consumer reads the explicit saved answer. |
| The user reviews individual business items in a dashboard | Report-owned DB approval | `window.report.updateField`/`updateFields` saves the item's status. An existing consumer route/step reads approved items later; the write alone starts nothing. |
| A report action should hand a specific task to the agent now | Report `sendChatMessage` | The action queues its message directly in an existing workflow chat, creating one only if none exists. Save an existing business approval first when the request depends on it. |
| The user corrects an execution already running | `send_step_message` | Forward to the exact active execution, live or at the next safe agent boundary. This does not start, restart, or resume completed work. |

`notify_user`, Daily Actions, and Pulse summaries are notifications/evidence,
not response or approval mechanisms. A notification delivery receipt does not
mean the human agreed. Route selection is also not blanket authorization for
every side effect on that route.

### Planned checkpoint versus an unattended run

Use `human_input` for a free-form value and a human-decided `branch` for a
fixed choice such as draft → approve/hold → action. Put the review before
the action it governs and provide the actual draft/change/evidence to review.
Do not add a checkpoint just to repeat a route choice or answer from chat.

- `execute_step(..., human_input=...)` can supply the user's known answer to
  that exact step. It is not permission for the builder to choose an answer.
- `run_full_workflow(..., human_inputs={step_id: answer})` requires responses
  for human-input steps on the selected path at launch. Use exact step IDs;
  use `route_selections` for routing and branch choices, not `human_inputs`.
- Do not pre-supply approval for output the user has not reviewed. Use an
  interactive human branch when the execution entry point supports waiting;
  otherwise run through the draft boundary, obtain the real answer, and then
  execute the consumer, or use durable proposal/approval/consumer stages.
- Schedules must complete unattended. For decisions that may take hours/days,
  persist the proposal, leave it pending, and end the producing run. A later
  authorized run/route consumes the answer. Do not hold a blocking call open
  until the next human visit.
- Never use skip-input/default values as evidence of human approval. The
  lower-level skip-input path currently has an `approved` fallback when no
  answer is supplied; that fallback is not consent. Require explicit approval
  evidence at the side-effect consumer, and surface missing approval instead
  of assuming every execution entry point enforces the same gate.

For execution arguments and completion/stop handling, Workshop/Run callers can
read `builder-reference`, `references/running-steps.md` when available.

### Urgent response card

Running workflow steps may use `human_feedback` for short-lived human-only input such as an OTP, CAPTCHA,
or an immediate decision that cannot wait. Ordinary Builder questions belong
in chat. The card is in-app only: it does not fan out through Gmail, Slack,
webhooks, or notification connectors.

Choose a realistic `timeout_seconds` within 30–1800 seconds (default 300).
On expiry, the action remains unapproved/incomplete; retry only while the
input is still needed. For bridge-only CLI calls, keep the tool call in the
foreground and let its response resume the same turn. Do not background it,
poll a file, or ask the user to send another message after answering. A shell
timeout must cover the requested wait. Cursor CLI's silent-call limit requires
`timeout_seconds <= 45`.

### Durable Pulse/review decisions

Use the existing question lifecycle, not a parallel approval table, for Pulse
coordination and technical/strategic review decisions. Attribute the request
to its real source (`technical_review`, `strategic_review`, or generic `pulse`).
For an authorized workflow change, supply the tool's structured `apply_contract`;
the consumer must not infer the repair from the question's prose alone.

An obsolete agent-issued pending proposal can be withdrawn by Builder using
`withdraw_human_input_request` with a reason and evidence references. The original
question, options and approval contract stay in history. This is agent judgment,
not an owner answer or permission to perform the proposal. Human-created requests,
suggestions and answered/claimed decisions are not eligible; provenance that is
unknown stays unknown. Pulse discusses withdrawal with Builder rather than
answering or withdrawing the owner's decisions itself.

The native card's controls have distinct meanings:

- **Ask in chat** opens a contextual discussion. It does not answer the question.
- **Save answer**, or an explicit final answer in chat recorded with
  `answer_human_input_request`, saves the answer. It does not itself apply it.
- **Take best action** explicitly delegates choosing and the resulting action.
  Respect that delegation's scope; do not treat an ordinary question as it.

Before applying, read the current decision with `get_human_input_request`,
check its exact option/scope and application contract, and use the appropriate
consumer. Use the approved-fixer tools when the contract requires a targeted
fixer. Record actual outcome/evidence before marking the answer consumed.
Do not write platform decision rows through a report's business-field API.

### Report-owned approval and report-to-chat handoff

Keep an existing business approval in the domain's existing record: for
example, WebsiteAEO's `audit_findings` status. Do not convert every business
item into a Pulse decision just to support a dashboard button.

Choose the button's behavior explicitly:

- **Approve** saves the business approval for its existing consumer.
- **Approve and apply** saves it, then sends a scoped report-to-chat request.
- **Ask agent** sends a question about the item without changing its approval.

`window.report.sendChatMessage(message, { requestId })` sends directly from the
user action without a popup or chat-choice step. It reuses a suitable
interactive workflow chat, or creates one only if none exists; a
running foreground turn keeps the message queued. It does not interrupt the
turn, take over a scheduled/view-only chat, or directly launch a route.

For apply requests, include the workspace item ID, actual proposal version or
equivalent stable evidence, approved scope, and exact intended route/consumer.
Ask the agent to re-read approval, skip already applied work, execute only that
item, verify the result, and refresh the report. A generic full-workflow run
may repeat the audit or approval gate and is not an equivalent handoff.

Await the DB save before requesting chat. A failed send leaves the approval
saved and must be retryable without rewriting it; its scheduled consumer may
still act later. A successful result says `queued`, not started/applied/completed. Per-view request IDs
reduce duplicate sends; durable already-applied checks still belong to the
consumer. Chat dispatch and approval saving are not one atomic transaction.

For API limits, receipt fields, error handling, and an authoring example, load
`read_skill(skills=[{"name":"builder-reference","path":"references/reporting-policy.md"}])`
in Workshop/Run when that reference is available. Report actions are unavailable
in headless preview and static published snapshots. Never send from rendering,
refresh, or polling callbacks. Chat dispatch does not change the report iframe's existing same-origin
isolation limitation.

### Correcting active work

When the user changes instructions during a running execution, use its actual
returned `execution_id` with `send_step_message`. `sent_to_cli` and
`queued_for_injection` describe delivery, not completed work. `no_active_agent`
means there is no messageable agent at this moment (for example validation or
script-only work); do not poll or start a duplicate execution. Stop requests
require the available stop tool, not merely an acknowledgement in chat.

### Make the human interaction concrete

For the selected pattern, identify: the exact item/output being reviewed;
where the answer is stored; which work pauses or ends pending; which route,
step, or chat receives it; and what counts as verified completion. Explain
those facts plainly to the user. Preserve existing authorization, separate
discussion from a final answer, and do not label saved/queued work as applied.
