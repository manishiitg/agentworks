[← Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-361 — Scheduled runs and background agents stopped before the steps they started finished

| Coordination | Value |
|---|---|
| State | Pushed to main (`0afb9f2c6`); local restart and RTS deploy pending; live check pending |
| Date | 2026-09-27 |
| Owner | scheduler-runs |
| Related subsystem | pulse-governance (Pulse reviewers), step-execution |

## Problem

Two symptoms, one cause: `execute_step` tells the agent "started in background, end your turn now, you will be notified", but the thing running that agent treated "the turn ended" as "the work is done".

**Scheduled runs.** salesoutreach's *Email outreach send* and *LinkedIn daily engagement* schedules are set up for 12 groups, but ran only the first group every day from Sep 19 to Sep 26. Their message asks the agent to loop over the groups with `execute_step`. The agent starts group 1 and ends its turn to wait for the result. Group 1 finishes; its notice is queued for the agent's next turn. But the scheduler's wait (`waitForConversationTurnTree`) only covers the turn and its child runs, so it declared the run complete (21 seconds) and immediately sent the Pulse finalizer message into the same session. The finalizer won the race; the "group 1 finished" notice landed in the finalizer's turn, which only backs up and reports. Groups 2–12 never ran: 6 verified emails and 20 researched LinkedIn contacts stayed unsent. Every run was recorded as a success, and Pulse labelled the gap "not directly repairable", correctly, since it was a platform bug.

**Background agents.** A `run_in_background` agent (e.g. a Pulse reviewer) that fixed a step and started a verification run obeyed the same instruction and ended its turn, but such an agent is finished once its turns end. The verification result went to the main session instead, and the reviewer never recorded its result, so the Pulse pass ended partial (social-media Technical Review, 2026-09-26: verification started 18:20, reviewer ended 18:22, Pulse finalized 18:24, verification finished 18:35). The steps it started were recorded with no parent at all, because tool calls arrive over the MCP bridge and lose the Go context.

## Definitions (after this change)

- **Turn completed:** one agent reply ended. Says nothing about whether the work is done.
- **Step completed:** owned by the step. A step waits for its own async sub-agents and hands their results back (`reconcileAsyncSubAgentCalls`) until a turn starts nothing new. Unchanged.
- **Scheduled message completed:** every step the run's turns started has completed, the agent has been given their results, and its last turn started nothing new.
- **Background agent completed:** the same rule, for the steps that agent started.

## Authorized change

One ownership rule at every level, modelled on the step's own sub-agent loop:

- `scheduled_turn_followups.go`: a scheduled run claims its session (`claimSessionCompletions`). After each turn it waits for every step started during the run, hands the agent their results as its next turn, and repeats until a turn starts nothing new; only then does it move to the next message or the Pulse finalizer. While claimed, the auto-notification path leaves completions and start notices for the run (`deferWorkflowStepAutoNotification`, `processBatchedBackgroundAgentStarts`) and the turn-tree waiter treats finished children as finished, so there is one deliverer and nothing to race. Bounded by the existing 3-hour ceiling and 100 rounds.
- `background_step_ownership.go`: each background agent's tool session maps to an owner record. `execute_step` finds the owner from the trusted MCP caller session (`mcpexecutor.SessionIDFromContext`), makes the step the agent's child, keeps its completion off the main session, and tells the agent the runtime will bring the result. After the agent's turns, `handOwnedStepResults` waits for its steps and continues the same conversation with their results until a turn starts nothing new.

## Acceptance and evidence

- A replay of the salesoutreach sequence (one group per turn, 12 groups) drives all 12, one results turn per group, and finishes only when a turn starts nothing (`TestScheduledRunDrivesEveryGroupToCompletion`).
- An owned session holds back the notification path; release is idempotent (`TestOwnedSessionHoldsBackTheNotificationPath`).
- A resumed thread's older runs don't hold the run open; a failed results turn keeps the result undelivered; a stuck step stops at the ceiling.
- A background reviewer gets its verification result as its next turn and can start and receive a re-run (`TestBackgroundAgentGetsTheResultOfTheStepItStarted`, `TestBackgroundAgentLoopsUntilATurnStartsNothing`); ownership is found only from the agent's own tool session and ends with it.
- All pass under `-race`. Full `step_based_workflow` passes; `cmd/server` passes apart from four catalog/prompt tests that fail identically on untouched origin/main.

Live acceptance pending: the salesoutreach email schedule reaching all 12 groups (sends real outreach within daily caps; needs the user's go-ahead), and a Pulse reviewer recording a verified result.
