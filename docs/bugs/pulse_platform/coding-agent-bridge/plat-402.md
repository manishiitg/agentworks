[← Platform issue index](../../pulse_platform_issue_register.md)

# PLAT-402 — Native agent tools always on for workflows and Relays (no switch, stored "off" ignored)

| Coordination | Value |
|---|---|
| State | fixed on `main` (`0cf68aa94`); deployed to Excellence (`agents-0cf68aa9`); RTS, Confida and Dominion deploy pending |
| Severity | P3 (consistency; a hidden setting changed what workflows could do) |
| Date | 2026-10-03 |
| Owner | coding-agent-bridge |
| Related | PLAT-396, PLAT-397 (what the native tools do in full mode) |

## Problem

Crew and Code always ran with native agent tools, but a workflow could still save `native_agent_tools: false` in its manifest. The server honoured it,
and after the switch was removed from the workflow Models page nobody could turn it back on.

## Done

- `nativeAgentToolsEnabled` (server) and its frontend mirror return true for any stored value; the manifest field still loads.
- The workflow Models page toggle is gone (`WorkflowCapabilitiesPanel`).
- Tests: `TestNativeToolsOnForEveryTurnTypeExceptSteps` (a stored off decides the same tools mode as an untouched workflow for every turn type),
  `TestWorkflowChatNativeAgentTools...` and `nativeAgentTools.test.ts`.

## Left

- Deploy to RTS, Confida and Dominion.
- Which workflows still have the old `false` stored was not listed; harmless, the value is ignored.
