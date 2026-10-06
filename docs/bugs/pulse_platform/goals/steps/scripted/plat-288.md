[← goals / steps / scripted](index.md)

# PLAT-288 — Inverted Workshop scripted fast-path mode gate

| Field | Value |
|---|---|
| State | deployed |
| Priority | - |
| Product | goals |
| Area | steps/scripted |
| Summary | `execute_step(fast_path_only=true)` advertises Workshop-only execution of a |

| Coordination | Value |
|---|---|
| Ticket state | Local fix tested; deployment reverify pending |
| Last synchronized | 2026-09-05 |
| Origin | build-in-public `PUL-FF23BCD5` |

`execute_step(fast_path_only=true)` advertises Workshop-only execution of a
saved script with no LLM fallback. The executor instead rejected the request
when the current mode **was** Workshop, before checking effective scripted
configuration or the saved file. It also returned that rejection as a successful
tool result. This is separate from PLAT-280's plan-step migration fix.

The executor now allows Workshop mode and returns a tool error for Run mode.
Downstream effective scripted-mode, saved-file and SavedScriptOnly checks remain
unchanged: missing/failed scripts do not gain an LLM fallback. No workflow code
or schedule was executed as part of this repair.

Verification: `TestWorkshopFastPathModeGate` checks Workshop, Run, unknown mode
and ordinary execution, and verifies the actual executor uses the gate. The
focused `Test.*Scripted` suite also passes. These are local source tests, not a
fresh deployed execution of the reported preflight step.

The exact SQLite finding is resolved for internal tracking with its prior record
preserved in a `platform_tracking_resolved` event. Deployment verification remains
pending; no historical run result was changed.
