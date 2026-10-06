[← goals / steps / routing](index.md)

# PLAT-151 — todo routes advertise `context_to_pass`, but runtime never delivers it to the child

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | goals |
| Area | steps/routing |
| Summary | P1 — plan authors believe a context handoff exists when no data |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | `implemented` — focused tests pass; existing plans normalize on their next managed write |
| Last synchronized | `2026-08-19` |

- **Priority:** P1 — plan authors believe a context handoff exists when no data
  channel is implemented, so isolated child agents repeat broad filesystem
  searches or work from incomplete evidence.
- **Owner:** todo-route plan schema and mutation tools in
  `planning_agent.go`; frontend plan types.

## Evidence and RCA

`context_to_pass` was stored, rendered in plan JSON, exposed by add/update route
tools, and included in changelog comparisons. It was not a `call_sub_agent`
parameter, was not included by `get_route_description`, and was never consumed
when the child session was created. In the live Social Media plan it contained
instructions such as "pass all prior route outputs", while the actual child had
no such payload.

There were therefore three apparently overlapping context concepts:

1. the sub-agent's standing `description`;
2. declared file `context_dependencies` that runtime really resolves/injects;
3. `context_to_pass`, which looked operational but was dead metadata.

Dynamic per-call context already has a real channel:
`call_sub_agent.instructions`.

## Fix and reasoning

Remove the third, non-functional channel. The contract is now:

- `sub_agent_step.description` — reusable standing brief;
- `sub_agent_step.context_dependencies` — exact durable file outputs the
  runtime resolves and injects;
- managed DB/KB tools — durable structured/narrative state when no file handoff
  is appropriate;
- `call_sub_agent.instructions` — current-run dynamic request.

The field is removed from backend/frontend types and mutation schemas. Old JSON
can still be read by normal JSON decoding, but the dead field is not retained
when the plan is next written through managed tools.

## Acceptance

1. Add/update route schemas do not advertise `context_to_pass`.
2. Marshaling a route cannot persist it.
3. A legacy route containing it still loads, while the child's declared
   `context_dependencies` remain intact.
4. Builder/reviewer guidance describes the real channels and flags missing
   dependencies based on semantics, not merely because an array is empty (DB/KB
   consumers may correctly have no file dependency).
