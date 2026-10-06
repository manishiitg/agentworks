[← goals / steps / scripted](index.md)

# PLAT-062 — the scripted prompt told the agent to write a path the folder guard forbids

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | goals |
| Area | steps/scripted |
| Summary | scripted-step execution prompt |

| Coordination | Value |
|---|---|
| Assigned agent | `Claude Code` |
| Ticket state | `implemented; runtime reverify` |
| Last synchronized | `2026-08-09` |

- **Priority:** P2
- **Owner:** scripted-step execution prompt
- **Found on:** hetznerssh, 2026-08-09, `create-final-security-report`

## Problem

One prompt carried two instructions naming two different write targets.

The MODE NOTE (`execution_only_agent.go:163`, in `executionOnlyUserTemplate`):

> *"Implement the task below as reusable Python code saved to
> `learnings/{step-id}/main.py`"*

The Code Execution Mode section appended to the **same** prompt
(`GetScriptedModeInstructions`, `controller_scripted.go:1538`):

> *"Write `main.py` (the entry point) to `<run>/code/main.py`"* …
> *"A passing `main.py` becomes the saved script for this step"*

`setupExecutionFolderGuard` opens exactly three write locations for a step: its
own run folder, `db/`, and `knowledgebase/notes/`. **`learnings/<step-id>/` is
never in `writePaths`** — deliberately, because the platform persists the script
itself after the turn (`controller_execution.go:2639`).

So the MODE NOTE named a path the step is structurally forbidden to write.

## Observed cost

`create-final-security-report` repaired its script, obeyed the MODE NOTE, was
correctly denied, recovered to `code/main.py`, and filed a concern:

> *"Persistent learnings path was read-only, so the fix was applied to the
> writable invoked `code/main.py`."*

**The report was wrong and the fix had persisted.** Timestamps:

| file | bytes | written |
|---|---|---|
| `runs/…/execution/create-final-security-report/code/main.py` | 22398 | 20:17:34 |
| `learnings/create-final-security-report/main.py` | 22398 | **20:18:16** |

Byte-identical, persistent copy written 42 s later by the platform. So the cost
was not data loss but a wasted turn plus a false finding that then consumes a
Pulse reviewer slot to dismiss.

## Why it surfaced only now

The step carried `learn_code_max_fix_iterations: 0` — a migration artifact
removed in PLAT-061 — so it had **never attempted a repair**, and this
contradiction had never been exercised. Removing that field did not create the
bug; it exposed a dormant one on its first run. Four sibling hetznerssh steps
carried the same 0 and are equally affected.

## Implementation (2026-08-09)

The MODE NOTE now names the run's own `code/main.py` as the write target, states
that the platform persists a passing script to `learnings/{step-id}/main.py`
after the turn, and says explicitly that **a denial on that path means the
contract is working, not that persistence failed** — the specific
misinterpretation that produced the false concern.

## Regression tests

`scripted_write_target_test.go`:

- `TestScriptedModeNoteDoesNotNameAReadOnlyWriteTarget` — the MODE NOTE must
  name `code/main.py`, and may mention the learnings path only while saying it
  is read-only and written by the platform.
- `TestScriptedInstructionsAgreeOnTheWriteTarget` — the two halves of the same
  prompt must keep pointing at the same place.

Full run reproduced the 22-failure baseline exactly; no new failures.

## Acceptance

A scripted step never attempts a forbidden write it was instructed to make.
Runtime reverify: the next hetznerssh run that repairs a script should complete
without a `learnings path was read-only` concern.
