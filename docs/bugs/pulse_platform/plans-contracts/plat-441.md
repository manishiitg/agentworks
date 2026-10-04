[← Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-441 — Relay agents call saved Python scripts as tools

| Coordination | Value |
|---|---|
| State | fixed on `main`; not deployed |
| Date | 2026-10-04 |
| Owner | plans-contracts |
| Related | PLAT-432 (scripted routes as named tools), PLAT-423 (retired Python tools), PLAT-431 |

## Owner decisions

- Relay agents may call saved Python scripts as tools (scripted routes). No
  sub-agents in Relays.
- Relays have no workflow database, knowledge base or learnings.

## Why it was blocked

Both "no routes on a Relay agent" rules came with the Relay MVP (2026-09-27)
without a recorded reason. In code: an agent with routes runs on the delegation
runtime, and `delegatingMessageSequenceAsOrchestrator` dropped `authored_prompt`
and `system_prompt`, so a Relay agent with routes would have lost its prompt and
JSON result contract.

## Done

- `OrchestratorPlanStep` carries `AuthoredPrompt`/`SystemPrompt` (runtime only)
  through the conversion, so the sequence executor keeps the authored prompt, the
  authored items and the JSON result check. The authored prompt is used verbatim
  plus an appended "Tools and sub-agents of this agent" block.
- An authored agent gets no generic delegation tools or executors
  (`call_sub_agent`, `call_generic_agent`, query/stop): only its named script tools.
- Plan validation no longer forbids routes on an authored agent. Relay validation
  allows only `type: regular`, `script_only: true` routes; sub-agents are refused.
  Publishing requires each route's saved `code/<route>/main.py`.
- Relays skip the knowledge-base and managed-DB migrations; the four DB tools
  PLAT-431 gave the Relay Builder (never deployed) are removed.
- Relay Builder prompt and skill describe script tools (no sub-agents, no
  workflow DB/KB/learnings; user systems via secrets).
- Tests: Relay validation, authored prompt surviving delegation, the tools block,
  Relay migration plan. `cli-step-contract` runs an authored agent that calls its
  script tool and answers JSON with the script's value.

## Left

- Runtime DB/KB/learnings removal was approved by the owner and implemented
  in [PLAT-447](plat-447.md). Deployment and live verification are tracked there.
