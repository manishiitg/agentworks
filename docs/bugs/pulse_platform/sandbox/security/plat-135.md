[← platform / security-sandbox](index.md)

# PLAT-135 — nothing now checks that a sub-agent cannot exceed the tool surface its product declared

| Field | Value |
|---|---|
| State | closed |
| Priority | P1 |
| Product | sandbox |
| Area | security |
| Summary | P1 — a containment property that used to be enforced by a test |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `open` |
| Last synchronized | `2026-08-18` |

- **Priority:** P1 — a containment property that used to be enforced by a test
  now has no test at all. Nothing is known to be broken; what is gone is the
  thing that would tell us if it broke.
- **Owner:** `cmd/server/agent_profile_runtime.go`, wherever sub-agents are
  spawned for workflow steps.

## What was lost

`TestDelegationUsesTheReadOnlyProfileLookup` asserted, against the source text
of `delegation.go`, two things:

1. delegation must not call `resolveAgentProfileForQuery`, which re-runs the
   whole product runtime initializer (workspace seeding, plan refresh, workflow
   DB init, `productdeps.Ensure`) for every sub-agent; and
2. delegation **must** call `lookupAgentProfileDefinition`, because without it
   *"the sub-agent would get a wider tool surface than the product declared."*

PLAT-134 removed chat delegation entirely — no `CreateDelegationTools`, no
`delegate` tool, and `executeDelegatedTask` (the only caller of
`lookupAgentProfileDefinition`) is gone. The assertion therefore had no subject
left and was removed in `413d8d32a`. `lookupAgentProfileDefinition` now has
**zero callers**.

## Why this still matters

Chat delegation is gone, but sub-agents are not. Workflow steps still spawn them
— `run_in_background`, `call_sub_agent`, message-sequence children, routed
sub-agent steps — and PLAT-134 explicitly preserved that machinery as workflow
orchestration rather than chat delegation.

The containment property is a property of *spawning a sub-agent*, not of the
chat path that happened to be the first to implement it. On the workflow path it
is currently unasserted: no test establishes that a step's sub-agent inherits a
tool surface no wider than its parent's product profile declared.

A source-text assertion was always a weak form of this. The replacement should
be behavioural — construct a parent under a restrictive profile, spawn a
sub-agent, and assert the sub-agent's registered tool set is a subset.

## Acceptance

- A test spawns a workflow sub-agent from a parent bound to a product profile
  with a restricted tool policy, and fails if the sub-agent registers any tool
  the profile did not declare.
- The test exercises the real spawn path rather than asserting on source text,
  so it survives the next refactor that moves the call.
- If `lookupAgentProfileDefinition` is genuinely dead, it is deleted as part of
  this work rather than left as an unused function implying a check that no
  longer happens.
