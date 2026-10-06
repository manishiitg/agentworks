[← platform / coding-agent-bridge](index.md)

# PLAT-002 — nested tool failures remain semantically successful

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P0 |
| Product | platform |
| Area | coding-agent-bridge |
| Summary | tool bridge, timing telemetry, and terminal status |

| Coordination | Value |
|---|---|
| Assigned agent | `Claude Code` |
| Ticket state | `runtime_reverify` |
| Last synchronized | `2026-08-04` |

> Claim this ticket in this file before implementation. During active work,
> update this fragment rather than the shared index; synchronize the index
> once at handoff, review, or completion.


- **Priority:** P0
- **Owner:** tool bridge, timing telemetry, and terminal status
- **Source findings:** `HARNESS-NESTED-ERROR-STATUS-PRECEDENCE` (Upwork) and
  `HARNESS-TOOL-ENVELOPE-ISERROR-2026-08-03` (Build-in-public), plus
  `HARNESS-TIMING-EMBEDDED-TOOL-ERROR` (Social Media, observed before the
  canonical implementation landed)
- **Problem:** the outer transport succeeds while the nested tool payload says
  `ERROR`, carries a non-zero nested exit code, or reports an HTTP failure.
  Stored traces still set `IsError=false` and `errored_count=0`.
- **Impact:** retries, alerts, validation, reviewers, and terminal status can
  treat a real failed operation as clean.
- **Important distinction:**
  [tool_failures_invisible_in_backend_logs.md](../../../tool_failures_invisible_in_backend_logs.md)
  fixed visibility with `[TOOL_ERROR]` logs and red UI rendering. It did not by
  itself make the canonical runtime/timing result an error.
- **Implementation (2026-08-03):** `mcpagent/toolerr` now has a narrow canonical
  classifier separate from the broad log-only suspect detector. The CLI stream
  adapter emits `ToolCallErrorEvent` instead of `ToolCallEndEvent` for nested
  failure envelopes, and saved CLI conversation history sets `IsError=true`.
  Sequential and parallel in-process tool paths use the same classifier.
  Problem-reporting/query tools are excluded from payload promotion so a
  returned domain row such as `status=failed` is not confused with transport
  failure.
- **Verification:** fixtures pass for nested `ERROR`, non-zero shell exit,
  permission denial, HTTP 4xx, `success=false`, and MCP `isError`; negative
  controls pass for prose discussing errors and historical failed DB rows. The
  real post-build timing artifact still needs to prove `errored_count` changes.
- **Current workaround:** agents and reviewers parse nested stdout/content and
  apply explicit error precedence themselves.
- **Acceptance:** fixtures for nested `ERROR`, HTTP failure, permission denial,
  and non-zero shell exit all set canonical error state, increment error counts,
  and prevent an unrecovered parent execution from being clean. Text merely
  discussing an error remains a success.
