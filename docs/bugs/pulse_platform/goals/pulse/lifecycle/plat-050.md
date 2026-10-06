[← goals / pulse / lifecycle](index.md)

# PLAT-050 — Pulse orchestration was split between the agent and Go

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | goals |
| Area | pulse/lifecycle |
| Summary | Pulse scheduler orchestration |

| Coordination | Value |
|---|---|
| Assigned agent | `Codex` |
| Ticket state | `implemented; runtime reverify` |
| Last synchronized | `2026-08-07` |

- **Priority:** P1
- **Owner:** Pulse scheduler orchestration
- **Source:** Upwork recovery-Fixer investigation and scheduler code audit

## Problem

Pulse reasoning was duplicated across two systems. The agent reviewed and fixed
workflow state, while Go selected reviewer sessions, launched an additional
residual Fixer, synthesized failure results, created recovery sessions, and
polled tmux every three seconds to guess when each stage had ended. A normal
review could therefore time out, be marked incomplete, and be repeated by a
second agent even after useful durable work had already been recorded.

## Fix

Pulse now continues in one main-agent session. The scheduler sends up to four ordered
turns: Gate, optional Review+Fix, Dashboard, and Finalize. The Review+Fix turn reads the
Gate worklist, chooses the cheapest sufficient review plan, owns any useful
specialist children, consolidates findings, applies safe fixes, verifies them,
and records one terminal receipt for every due module. Missing receipts cause a
compact continuation in the same conversation, never a residual or recovery
agent.

Go retains only transport and integrity responsibilities: start/resume the
session, preserve ordering, expose normal Workflow Builder tools, validate the
durable worklist and stage receipts, validate/rollback the dashboard artifact,
and reconcile final-command status. Pulse stage completion is driven by the
session event stream with a short idle-settle boundary; it no longer captures
tmux panes every three seconds. If a turn exceeds its inactivity boundary while
the underlying agent is still live, the scheduler fails the Pulse pass partially
and refuses to send another message into that conversation; it does not overlap
turns or create a recovery session.

`/engineering-review` uses `pulse_run_id="current"`, resolved to its current
Workflow Builder session, then records the normal complete Gate worklist before
Review+Fix. The manual `/pulse` command was removed in favor of the Pulse play
action, which runs the normal pipeline. The old selected-module
`begin_pulse_fixer_run` path, temporary identity lease, recovery-terminal
projection, and scheduled stale dedicated Goal Advisor instruction were removed.

The former standalone `run_goal_advisor_review` Advisor→Critic→Finalizer
pipeline was removed. The `/goal-advisor` slash command now uses ordinary
guided background review; scheduled Pulse uses the Goal Advisor perspective in
its normal Review+Fix conversation.

The remaining `pulse_run_id` is now only a durable correlation key. Pulse has
no trusted-session registry, expiry, delegated child-writer grant, or separate
reviewer identity. The active workflow conversation remains the normal writer
boundary. Go also no longer rejects a Gate worklist merely because it contains
more than two due perspectives: cost selection is guidance for the Gate agent,
not a hidden scheduler policy.

## Verification

- Scheduler contract tests pin exactly four normal Pulse turns.
- Prompt tests require agent-owned selection, delegation, repair, verification,
  and per-module terminal receipts.
- An event-driven completion test proves Pulse advances after a runtime event
  and performs zero tmux captures.
- A focused ordering test pins that a failed or timed-out live turn stops the
  sequence before another message can start in the same conversation.
- Focused server tests pass. A real scheduled Pulse pass must still verify that
  long-running child delegation settles and resumes the same parent session.
