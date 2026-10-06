[← goals / steps / database](index.md)

# PLAT-162 — HTTP-backed workflow tools publish argument schemas but execute unvalidated maps, allowing guessed field names to reach handlers and trigger retry storms

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | goals |
| Area | steps/database |
| Summary | P1 — malformed plan edits do not silently persist, but one |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | `implemented` — focused contract tests pass; runtime restart/reverify pending |
| Last synchronized | `2026-08-20` |

- **Priority:** P1 — malformed plan edits do not silently persist, but one
  ordinary Builder turn spent repeated calls guessing the same tool contract,
  consuming time/cost and leaving final validation incomplete.
- **Owner:** mcpagent direct-tool registration/execution contract and
  AgentWorks plan-tool schemas.
- **Source session:** Sales Outreach Workflow Builder
  `c2b1bb4c-5a4d-4fd1-9709-ad7329c34a7b`.

## Evidence and root cause

`update_message_sequence_step` already published `existing_step_id` and
`reason` as required fields. The coding CLI nevertheless reached the HTTP
custom-tool executor with a missing/guessed identifier, and the handler saw an
unchecked `map[string]interface{}`. It therefore reported `step ID '' not
found` instead of rejecting the invocation as missing `existing_step_id`. The
agent retried the same operation repeatedly with guessed shapes.

`validate_plan_change` had a related contract drift: its handler required at
least one of `forbidden_references` or `expected_context_dependencies`, while
its published schema expressed neither requirement. Empty calls therefore
reached the handler and failed only after dispatch.

The platform did have `get_api_spec`, but discovery was advisory. Direct LLM
tools received the schema and HTTP callers could retrieve it, while the shared
executor enforced neither copy. The transports therefore did not implement one
tool contract.

## Fix

mcpagent now wraps every registered direct/custom executor with validation of
the same parameter schema used by direct provider calls and `get_api_spec`.
The wrapper runs before the handler for direct, structured, and HTTP bridge
execution because all three resolve the canonical registered executor.

Root argument objects are closed by default. Missing required names, unknown
names, root value types, enums, minimum string length, and `anyOf` required-field
sets produce one deterministic error naming the exact problem, listing expected
fields, and pointing HTTP callers back to `get_api_spec`. A tool that genuinely
accepts extension fields can explicitly declare `additionalProperties`.

AgentWorks now publishes the handler's real `validate_plan_change` alternative
requirement through `anyOf`, and declares a non-empty `existing_step_id` for
`update_message_sequence_step`. Go still does not interpret the desired plan
change: the agent owns design and reasoning; the platform only enforces tool
invocation syntax.

## Verification

- focused mcpagent tests prove malformed arguments never reach the executor,
  the error reports both missing and unknown fields, valid calls still execute,
  and `validate_plan_change` rejects an empty argument object from its schema;
- mcpagent `executor` and `agent/codeexec` packages pass;
- focused AgentWorks plan-change/workshop/message-sequence tests pass;
- the full AgentWorks step package still has two unrelated pre-existing source/
  prompt invariant failures; the full mcpagent agent package is gated by four
  existing real-provider fixtures awaiting manual review verdicts.

## Acceptance

1. `get_api_spec`, direct tool definitions, structured transports, and the HTTP
   bridge use one registered schema.
2. `{"step_id":"x"}` sent to `update_message_sequence_step` fails once with
   `missing existing_step_id; unknown step_id`; the handler is not invoked.
3. `{}` sent to `validate_plan_change` fails before its handler and identifies
   the two valid alternatives.
4. Correct calls execute unchanged and the agent remains the sole owner of plan
   design decisions.
