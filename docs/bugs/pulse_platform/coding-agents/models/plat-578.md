[← coding-agents / models](index.md)

# PLAT-578: Stale AGY alpha-gate assertion fails the workflow test suite

| Field | Value |
|---|---|
| State | open |
| Priority | P2 |
| Product | coding-agents |
| Area | models |
| Summary | Workflow test still expects AGY_ALPHA gating after the shared provider guard accepts supported coding CLIs. |

## What happened

The full step_based_workflow Go test suite fails only
`TestValidateStepLLMConfigEnforcesAgyAlphaGate`: "AGY accepted without alpha flag".
The unchanged `pkg/llmguard.RequireCodingAgentProvider` accepts registered coding
CLI providers, including agy-cli, without an AGY_ALPHA check. The test still
expects the retired alpha policy. Found while verifying PLAT-577; no Relay
change touches that guard or the failing test's validation path.

## Fix

No code change in this ticket.

## Left

Confirm the intended current AGY availability policy and reconcile the stale
test with that policy. Do not reintroduce an alpha restriction just to make this
unrelated Relay change pass.
