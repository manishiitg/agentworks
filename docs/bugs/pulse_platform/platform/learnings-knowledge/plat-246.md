[← platform / learnings-knowledge](index.md)

# PLAT-246 — Workflow Builder lacks actionable scripted-use guidance for the active text and web-search tools

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | platform |
| Area | learnings-knowledge |
| Summary | harness issue, severity medium. |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | `implemented; runtime reverify` |
| Last synchronized | `2026-08-29` |

- **Priority:** harness issue, severity medium.
- **Findings:** No production workflow failure is linked yet. The gap was found
  while refactoring the shared provider-backed agent surface to retain only
  `generate_text_llm` and `search_web_llm`.
- **Related:** [PLAT-244](../security-sandbox/plat-244.md)) (narrowed tool surface and restored
  reference pointer); [PLAT-234](../coding-agent-bridge/plat-234.md)) (search timeout guidance).

## Problem

The Builder/Workshop prompt correctly exposes the two active tools and points
agents at `builder-reference/references/workspace-media-tools.md`. That
reference documents the signatures and valid enum values, but it does not give
a workflow author or scripted step enough decision guidance to use the tools
well:

- when fresh, external evidence warrants `search_web_llm` instead of ordinary
  reasoning or an already-available source;
- when a bounded second model call warrants `generate_text_llm`, and how to
  choose `low`, `medium`, or `high` based on task difficulty and cost;
- that a script must invoke these tools through the authorized MCP bridge,
  following the `mcp-bridge` reference, rather than constructing raw requests
  or embedding credentials; and
- minimal success, timeout, provider-failure, and evidence-recording patterns.

Without this, agents know the API names but must infer operational policy. That
invites avoidable direct-answer behavior, unprincipled tier selection, and
unsafe shell/credential workarounds.

## Fix

`workspace-media-tools.md` now gives active-tool decision rules: use search
for fresh external evidence, use text generation for a bounded additional model
operation, and choose low/medium/high according to transformation complexity,
normal synthesis, or material reasoning/quality risk. It also records returned
provider/model identity as runtime evidence rather than assuming a permanent
tier-to-model mapping.

The same reference now has a scripted-workflow section: inspect granted tools
and their current schema, read `mcp-bridge.md`, and invoke only the custom tool
through `$MCP_CUSTOM`. It forbids direct provider calls, invented endpoints,
raw keys, and passing `model_id` to hosted-MCP search.

Both the compact Builder prompt and the coding-CLI pointer now explicitly tell
scripted/code-execution agents to load both references before writing the
bridge call.

## Acceptance criteria

## Verification

- `TestSpecialWorkspaceToolsInstructionsDirectScriptedAgentsToBridgeGuidance`
  pins the compact prompts' two references and direct-provider/key prohibition.
- `TestScriptedActiveProviderToolsReceiveBridgeSafeGuidance` proves a scripted
  step granted both active tools receives both the active-tools and MCP-bridge
  references, including the exact `$MCP_CUSTOM` routes.
- Focused guidance, instruction, and workflow prompt tests pass; focused lint
  passes for `pkg/instructions` and `cmd/server/guidance`.

## Reverify

Start a fresh Workshop session with each tool separately and a scripted step
with both. Confirm the agent reads the attached guidance, uses the MCP bridge
for a real call, records/cites returned search evidence where relevant, and
chooses a text tier consistent with the documented task class.
