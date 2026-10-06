[← goals / step-execution](index.md)

# PLAT-006 — workflow-step shell cwd disagreed with its contract

| Field | Value |
|---|---|
| State | closed |
| Priority | P1 |
| Product | goals |
| Area | step-execution |
| Summary | workflow step session/bridge context |

| Coordination | Value |
|---|---|
| Assigned agent | `Claude Code` |
| Ticket state | `runtime_reverify` |
| Last synchronized | `2026-08-04` |

> Claim this ticket in this file before implementation. During active work,
> update this fragment rather than the shared index; synchronize the index
> once at handoff, review, or completion.


- **Priority:** P1
- **Owner:** workflow step session/bridge context
- **Legacy source:** RTS Latency `step-daily-latency-collect-dev-voice` finding,
  currently `awaiting_verification`
- **Incident:**
  [workflow_step_shell_working_directory.md](../../../workflow_step_shell_working_directory.md)
- **Problem:** a dedicated child shell ran from the run execution folder while
  some prompts/skills claimed docs-root cwd; the earlier inverse failure also
  existed when dedicated sessions lost their run cwd.
- **Current state:** code now assigns a workflow-step run cwd directly and
  fails closed when it is absent. The remaining finding must be replayed on the
  rebuilt runtime to distinguish stale guidance from a runtime regression.
- **Acceptance:** regular, message-sequence, todo, reviewer, and Fixer session
  tests state their cwd contract explicitly and observe exactly that directory.
  Guidance contains no contradictory relative-path examples.
