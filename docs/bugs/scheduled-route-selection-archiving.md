# Scheduled route selection lost during forward navigation

## Local incident — 2026-10-05

The trading workflow's News Briefing schedule requested `step-weekly-propose-archetypes=monitor` for `iteration-123-sched/default-group`. The expected tick was 07:30 IST; scheduler logs show dispatch at 07:39:58 and `LATE_FIRE` drift of 9m59s. The branch evaluated at 07:56:35. The chat's claim that the scheduler itself fired 26 minutes late conflated branch evaluation time with schedule dispatch time. The available evidence does not establish the cause of the late dispatch.

The first step wrote a time-window probe selecting `propose`, then followed `next_step_id` to the branch. `runExecutionPhase` had already seeded the explicit `monitor` selection into the branch's execution folder. `navigateToNextStepID` archived the target and downstream step folders, moving that input into `execution/archived/run-1/step-weekly-propose-archetypes/route_selection.json`. The branch no longer found its preseed and selected `propose` from the probe's `route_source_file`; its routing-evaluation.json records that source and choice. The assistant later dispatched the monitor steps separately to recover the missing briefing.

This is a runtime bug, not an intended rule that branch steps ignore caller selections. Time-based plan workarounds cannot reliably preserve an explicitly selected schedule route.

## Fix

For file-driven routing and branch steps, `resolveDeterministicRoutingSelection` now reads the execution options' explicit `route_selections` directly before consulting disposable step artifacts or source files. The choice survives forward jumps, archival, and re-execution, and the runtime persists `source_kind=route_selections` in the chosen route's evidence. Invalid explicit selections fail instead of silently falling back. Relay `value_path` decisions retain their existing authoritative input-value semantics.

Regression coverage drives the real branch executor with a missing preseed and a conflicting proposal file, checks route ID and next-step-ID choices, verifies recorded routing evidence, and rejects an unknown explicit route. No live schedule, plan, trading data, or execution was changed during verification.
