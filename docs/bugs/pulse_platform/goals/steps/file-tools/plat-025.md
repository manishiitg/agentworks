[← goals / steps / file-tools](index.md)

# PLAT-025 — workspace shell buffers stdout without a memory bound

| Field | Value |
|---|---|
| State | open |
| Priority | P1 |
| Product | goals |
| Area | steps/file-tools |
| Summary | `workspace/handlers/shell.go` subprocess output capture |

| Coordination | Value |
|---|---|
| Assigned agent | `Claude Code` |
| Ticket state | `queued` |
| Execution order | `D — after A, B, and E` |
| Last synchronized | `2026-08-04` |

> Claim this ticket as `in_progress` before implementation. Update this
> fragment during active work; synchronize the shared index at handoff.

- **Priority:** P1 availability
- **Owner:** `workspace/handlers/shell.go` subprocess output capture
- **Evidence:** the handler accumulates complete stdout in `stdoutBuf` before
  the downstream agent-facing cap can run.
- **Source:**
  [what_the_runtime_tells_an_agent_about_itself.md](../../../../what_the_runtime_tells_an_agent_about_itself.md)
- **Problem:** a runaway or extremely verbose command can grow server memory
  without bound. Applying the existing downstream truncation blindly would
  corrupt scripted steps that require complete schema-validated JSON.
- **Implementation boundary:** introduce bounded capture/spooling with an
  explicit full-output path for consumers that require it. Do not silently
  truncate scripted-step results.
- **Acceptance:** a large-output fixture keeps resident capture bounded,
  agent-facing output follows the existing head/tail contract, scripted JSON is
  either delivered completely or fails explicitly, and cancellation removes
  any temporary resource.
