[← Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-366 — Crew-only tokens can read workflow function results through crew polling

| Coordination | Value |
|---|---|
| State | Fixed on `main` in `39b468a8b`; not yet deployed to RTS |
| Date | 2026-09-28 |
| Priority | P1 |
| Owner | security-sandbox; implementation unassigned |
| Related subsystems | external MCP/CLI, crew/workflow functions |
| Reviewed revision | `ebc37cf87` on main, including the local changes present during review |

## Problem

`get_crew_function_call` can return a workflow function call to a token with
only `crews:read`. The caller must be the same user who owns the call and
know its call ID. This is a token-scope bypass; cross-user access and call-ID
enumeration were not demonstrated.

The crew and workflow endpoints share `crewFunctionCalls`. The crew poller
checks `call.UserID`, `CallerKind`, and `AllowsCrew(call.TargetID)`, but never
checks `call.TargetKind == triggerCallerCrew`. With `AllCrews=true`, a workflow
ID passes this crew-bound check. The request bypasses the workflow endpoint's
workflow scope and access resolution and returns the result and target path.

Evidence:

- [external_crews.go](../../../../agent_go/cmd/server/external_crews.go): `externalCrewCall`, lines 144–158 at review.
- [crew_functions.go](../../../../agent_go/cmd/server/crew_functions.go): shared registry and lookup, lines 282–290.
- [external_tools.go](../../../../agent_go/cmd/server/external_tools.go): crew dispatch happens before workflow resolution, lines 306–308.
- [external_workflow_functions.go](../../../../agent_go/cmd/server/external_workflow_functions.go): workflow poll explicitly checks target kind and workflow ID.

## Reproduction

1. Seed a completed function call owned by `owner`, with
   `CallerKind=user`, `TargetKind=workflow`, `TargetID=private-workflow`,
   and a synthetic result `workflow-private-result`.
2. Authenticate as `owner` using a token with only `crews:read` and
   `AllCrews=true`, with no workflow scopes or workflow grants.
3. Invoke the full external dispatcher with
   `get_crew_function_call(call_id=<the workflow call ID>)`.
4. Expected: not found / denied. Actual: HTTP 200 with the workflow result.

Review-only regression `TestReviewCrewOnlyTokenCannotReadWorkflowCall`
failed with:

```text
crew-only token read workflow result without workflow scope:
{"call_id":"review-workflow-call",...,"result":{"answer":"workflow-private-result"},
 "target":{"kind":"workflow",...,"workspace_path":"Workflow/private"}}
```

The probe used synthetic state and the real external dispatcher. No real
private result was retrieved and no production exploitation is claimed.

## Proposed fix and acceptance

Reject calls of the wrong target kind before applying target-specific grants.
Revalidate current access to the actual crew before exposing the record. Use
the same authorized resolver for future call-scoped answer endpoints.

- [ ] A crew-only token cannot read a workflow call, including when crew and
  workflow IDs happen to match and when `AllCrews=true`.
- [ ] A workflow-only token cannot use crew polling to retrieve crew results.
- [ ] Another user's call remains inaccessible.
- [ ] A revoked target grant or removed crew access denies subsequent polls.
- [ ] Authorized crew polling and authorized workflow polling still succeed.
- [ ] A regression exercises the external dispatcher, not just the helper.

Related:
[PLAT-369](../integrations/plat-369.md) must preserve this boundary when adding
call-scoped questions and answers.

## Fix (2026-09-28)

Fixed in `39b468a8b`. `get_crew_function_call` now requires the call to
target a Crew and re-resolves that Crew under the caller's current access,
so revoked access also stops polling. Workflow calls stay on
`get_workflow_function_call`. The not-found text no longer claims calls
vanish on restart (see PLAT-370).

Test: `external_crew_call_scope_test.go` (a Crew-only token cannot read a
workflow call). Not yet deployed to RTS.
