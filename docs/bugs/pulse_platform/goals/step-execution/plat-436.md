[← goals / step-execution](index.md)

# PLAT-436 — Scripted steps never self-heal in a run

| Field | Value |
|---|---|
| State | deployed |
| Priority | - |
| Product | goals |
| Area | step-execution |
| Summary | fixed on `main`, not deployed: runs execute the saved script and fail on its error; only the Builder's own `execute_step` authors or repairs scripts. |

| Coordination | Value |
|---|---|
| State | fixed on `main`; not deployed |
| Date | 2026-10-04 |
| Owner | step-execution |
| Related | PLAT-432 (named route tools; the run that exposed it), PLAT-428 |

## Decision (owner)

Scripted steps should not self-heal at run time. If a script fails, the step
(and the workflow) fails and Pulse reports it. Writing and repairing scripts is
a deliberate Builder action.

## What runs did before

At run time a scripted step could reach an LLM in four ways: no readable saved
script (an LLM wrote one from scratch, which also happened when a folder-guard
denial hid an existing script), a failed script (up to 3 LLM repair rounds), a
`lock_code` step (an LLM "recovery turn", then agentic fallback), and plain
retries. The repair LLM can write anywhere under `code/`, which most plausibly
emptied a route's saved folder in the PLAT-432 investigation (inferred).

## Done

- `scriptedRunIsStrict`: every run is strict (run the saved `main.py`, fail with
  its error); the existing saved-script-only path does the work. Only
  `ExecutionContext.AllowScriptRepair` lifts it, set solely by the Builder's own
  `execute_step` in Workshop mode, outside a scheduled session, without
  `fast_path_only`, and never when the controller's run kind is a schedule,
  Slack or webhook. `script_only` steps stay strict for the Builder too.
- Routes called by an agent, `run_full_workflow` (also from the Builder chat),
  schedules and Relay/webhook runs are runs.
- `lock_code` now only stops the Builder's own repair; tool and guidance text
  updated; code-authoring says runs never repair scripts.
- Unit test `TestScriptedRunIsStrictUnlessTheBuilderRepairs`.

## Follow-up 2026-10-04: Builder guidance

The Builder's guidance still described the old behaviour in several places.
Fixed: `scripted.md` now opens with "Scripts are built by you, not healed by the
run" (runs execute the saved script and fail on its error; Pulse reports it; the
Builder writes and repairs with `execute_step`); `optimize-playbook.md` no longer
says `lock_code` stops "the fix loop / execution agent" in runs (four places: it
now only stops the Builder's own `execute_step` repair, and advises locking late);
`code-authoring.md` drops "autofix sees it" and scopes the exit-code-2 rule to the
Builder's own runs; `workflow-tools.md` says only the Builder's `execute_step`
may repair. Test: `scripted_no_self_heal_test.go` fails if any of the old
promises reappears.

`docs/workflow/learn_code_flow.md` (the scripted execution description) now opens
with the run-time rule and scopes generation, repair, save-back, the `lock_code`
effect and the `code_exec` fallback to the Builder's own `execute_step`.

## Left

- Live: `cli-step-contract` (saved scripted step + named route, strict by
  default) PASS on claude-code, 2026-10-04. Not yet live-checked: a failing script
  failing the step with no LLM turn, and Builder `execute_step` authoring a new
  script.
- Pulse reporting of the failure is the existing step-failure path; not
  re-verified here.
- `TestGetRelayCommandCatalogWithoutGenericRuntimeRegistration` fails on current
  main, unrelated to this change.

## Register notes

[PLAT-436](plat-436.md), fixed on `main`, not
deployed: runs execute the saved script and fail on its error; only the
Builder's own `execute_step` authors or repairs scripts.
