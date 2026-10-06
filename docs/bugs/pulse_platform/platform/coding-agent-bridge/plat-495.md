[← platform / coding-agent-bridge](index.md)

# PLAT-495 — A structured Codex step was killed mid-turn by another Codex run's completion in the same folder

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | platform |
| Area | coding-agent-bridge |
| Summary | fixed on main: the structured completion tracker is bound to the run's own thread. |

| Coordination | Value |
|---|---|
| State | fixed on main (provider `01a1257`, pinned in the builder); needs a restart |
| Date | 2026-10-05 |
| Owner | coding-agent-bridge |
| Related | PLAT-108 (the "remaining gap" named in the code comment) |

## Source

Website workflow, schedule "Website analysis", 2026-10-05 10:31: step 4 (`fetch-rankings-and-brand-consensus`, Codex `gpt-6.1-sol`, structured
`codex exec --json`) failed after 2 minutes with `codex run failed: exit status 1: Reading additional input from stdin...`.

## Cause

The structured adapter has a second completion signal: the rollout file records `task_complete`. It found the rollout by WORKING FOLDER only (no thread
binding). The schedule's own Codex chat session (interactive, started 10:29:46) runs in the same folder; its turn finished at 10:31:48, the tracker took that
for the step's own completion and tore the step's process down mid-turn (`codex: rollout reported task_complete without a turn.completed on stdout`);
the killed process exited 1, which was recorded as the step's failure. Not the model name (`gpt-6.1-sol` and `gpt-6-luna` both work, 0.160.0) and not the
native-shell flags (checked with `codex exec`).

## Done

- The tracker now resolves only the rollout of the thread the run announced on stdout (`codexOwnThreadRolloutResolver`). Until the thread id is known or its
  rollout exists, nothing completes; the stdout `turn.completed` stays the primary signal. One regression test; a real structured run still completes.

## Left

- Not reproduced end to end: the failure needs a concurrent Codex chat session in the same folder (it happened once). Re-run "Website analysis" and watch for
  `tearing down pid` lines.
- Re-run the failed step (`Website analysis`, iteration-18-sched) if it was not recovered.

## Register notes

[PLAT-495](plat-495.md), fixed on main: the structured completion tracker is bound to the run's own thread.
