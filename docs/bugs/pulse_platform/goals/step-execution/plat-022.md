[← goals / step-execution](index.md)

# PLAT-022 — `get_api_spec` is absent from a workflow-phase session

| Field | Value |
|---|---|
| State | closed |
| Priority | P1 |
| Product | goals |
| Area | step-execution |
| Summary | workflow-phase custom-tool registration and mcpagent registration |

| Coordination | Value |
|---|---|
| Assigned agent | `Claude Code` |
| Ticket state | `assigned` |
| Execution order | `A — first` |
| Last synchronized | `2026-08-04` |

> Claim this ticket as `in_progress` before implementation. Update this
> fragment during active work; synchronize the shared index at handoff.

- **Priority:** P1
- **Owner:** workflow-phase custom-tool registration and mcpagent registration
  materialization
- **Evidence:** three occurrences of `custom tool get_api_spec is not registered
  for session msgseq-iteration-0-job-search-…-step-4`.
- **Source:**
  [what_the_runtime_tells_an_agent_about_itself.md](../../../what_the_runtime_tells_an_agent_about_itself.md)
- **Problem:** this is not the existing category/allow-list defect. The tool is
  absent from the session registry, so discovery cannot succeed regardless of
  which category or request shape the agent uses.
- **Implementation boundary:** trace workflow-phase tool construction through
  final registration and identify why this specific message-sequence step loses
  `get_api_spec`. Do not weaken registration or authorization globally.
- **Status (2026-08-04) — not closed, correctly flagged by Codex review:**
  the log evidence for the specific `msgseq-iteration-0-job-search-…-step-4`
  session was rotated away by a server restart before it could be traced to a
  registration call site. Investigation instead found and fixed a same-shape
  defect: `workflow-tools.md`'s opening `get_api_spec` instruction was
  unscoped to code-execution mode, so a native-provider `workshop`-mode
  session reading it would hit the identical error
  (`workflow-tools_get_api_spec_scope_test.go`, commit `676c525d0`). That is
  real but adjacent — it does not reproduce or repair the original
  message-sequence registration path, and no workflow-phase fixture or real
  job-search run has confirmed the marker is gone. Ticket state intentionally
  left as `assigned`, not advanced to `implemented`.
- **Acceptance:** a workflow-phase/message-sequence fixture registers
  `get_api_spec`, can retrieve the spec for an allowed tool, and still denies a
  genuinely ungranted tool. A real job-search step no longer emits the
  not-registered marker.
