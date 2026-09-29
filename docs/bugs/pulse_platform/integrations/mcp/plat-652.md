[← integrations / mcp](index.md)

# PLAT-652: MCP elicitation for function-call questions

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | integrations |
| Area | mcp |
| Summary | Crew and workflow function questions arrive as MCP form elicitation for clients that declare it; others keep poll/reply. |

## What happened

A Crew or workflow function called over MCP keeps running after the call
returns. When it asks a question, the caller only saw it by polling
`get_*_function_call` and answering with `reply_*_function_call`. MCP
2026-07-28 clients can take the question as a form (`input_required`), but
AgentWorks never offered one. The feature was written on branch
`codex/mcp-elicitation` (88e68395c) and never merged or run end to end.

## Fix

Design: [docs/design/mcp_elicitation.md](../../../../design/mcp_elicitation.md).

- The hosted endpoint (`/api/external/v1/mcp`) and the local `agentworks`
  stdio bridge return an `input_required` form for the first pending,
  answerable question when a call/poll result has one and the request
  declares the `elicitation` capability. The form has one `response` string;
  exact-choice questions use an enum.
- The client retries with `requestState` + `inputResponses`. The answer goes
  through the existing `reply_*_function_call` dispatcher, then the call is
  polled again. Both go through the REST dispatcher with the current
  request's credentials, so every retry is re-authorized; `requestState` is
  only a routing hint. A retried `call_*` never starts a second call.
- No form for: clients without the capability, legacy (pre-2026-07-28)
  clients on the stateless hosted endpoint, workflow readers
  (`can_reply: false`), connections without the reply tool, and questions
  with credential or one-time-code wording (MCP forbids sensitive data in
  form mode). Those keep `pending_inputs` + reply.
- Merging onto main: one shared implementation
  (`agentworksclient.FunctionElicitationHost`) now serves both the hosted
  endpoint and the bridge (the branch had two copies of the security
  checks); the secret-wording list was widened (passphrase, bearer, MFA,
  security/login code, SSH key, card number and others).

Verified live 2026-10-07 on an isolated local server (ports 19843/19844,
own state root, claude-code provider) with the mcp-go v1.0.0 client over
Streamable HTTP, workflow `ask` function told to ask "Which branch should
I deploy?" with options main/release:

- Modern client with elicitation: a poll returned `resultType:
  input_required` with an `elicitation/create` form (enum main/release); the
  client's handler answered "release", the SDK retried with
  `requestState`/`inputResponses`, and the run finished with "You chose
  release."
- Modern client without the capability, and a legacy (2025-11-25) client
  that declared elicitation at initialize: no form; `pending_inputs` showed
  the question, `reply_workflow_function_call` answered "main", run finished
  with "You chose main."
- Claude Code 2.1.292 (`claude -p` with a throwaway `--mcp-config`): no form
  appeared; it saw `pending_inputs`, answered with
  `reply_workflow_function_call`, and the run finished with the answer.

Cross-user answers, changed call/workflow IDs, invalid choices, decline, a
question arriving after the first poll, and replayed answers are covered by
`external_mcp_elicitation_test.go` (not repeated live).

## Left

- Crews cannot raise these questions today: the Crew (Work) product does not
  enable `human_feedback`, so a live `ask_crew` reported it had no ask-user
  tool. The Crew path works in tests only; it becomes live if Crews get a
  question tool.
- Not verified with a client that shows elicitation forms: interactive
  Claude Code, Claude.ai/Cowork, ChatGPT.
- A declined form leaves the question pending, and the next poll offers the
  form again.
- Not deployed.
