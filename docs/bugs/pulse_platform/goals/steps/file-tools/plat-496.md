[← goals / steps / file-tools](index.md)

# PLAT-496 — Step agents fail runs by claiming a "read-only filesystem" without trying to write

| Field | Value |
|---|---|
| State | open |
| Priority | - |
| Product | goals |
| Area | steps/file-tools |
| Summary | open: agents report "read-only" without trying to write; the platform allowed the writes each time. |

| Coordination | Value |
|---|---|
| State | open (found 2026-10-05, seen 3 times in 9 days; no platform permission error in any of them) |
| Date | 2026-10-05 |
| Owner | step-execution |

## Source

Message-sequence step agents (Codex `gpt-6.1-sol` in both recent cases) end an item with `STATUS: FAILED` saying the session's filesystem is read-only, and the whole
run fails:
- 2026-10-05 01:32 Upwork workflow, `toptal-submit`: one tool call (a directory listing showing the folder writable), then "read-only filesystem permissions prevent
  creating `toptal_submit.json`". No write was attempted. The day had nothing to submit (`no_job`). A recovery later wrote the file.
- 2026-10-05 10:36 salesoutreach, group `india-engineering-ops`, `step-prepare-linkedin-engagement` item `quality-recheck`: "the session's read-only filesystem policy
  prevents creating the required `engagement_prep.json`". Failed the schedule "LinkedIn daily engagement" at 10:44.
- 2026-10-05 11:28 Upwork "Twice-Daily Upwork Search + Bid", step `bid-read-and-draft` item `read-job-brief`: "the session's read-only filesystem restriction prevents creating `job_brief.json`".
  Third occurrence today (01:32, 10:36, 11:28). The schedule's stored `workshop_mode: "run"` was suspected and changed to `workshop` by the Upwork chat: it is a legacy field the scheduler
  ignores (`scheduledWorkshopTurn.workshopMode()` always returns "workshop"; 39 of 48 schedules carry it), so that is NOT the cause and the change did nothing.
- 2026-10-04 `enrich-shortlist` (Upwork) and 2026-09-27 (Upwork, schedule.log) the same claim.

## What is known

- The platform allowed the writes: the session's `FOLDER_GUARD_RESOLVE` WritePaths include the step's output folder in both cases. No `EROFS`, "Read-only file system",
  "Operation not permitted" or "Permission denied" is logged near either. The same runs' other steps wrote files normally.
- Both predate the cause I could think of: the 01:32 case ran before today's native-shell change (PLAT-491), so that change is not the cause of that one. The 10:36 case
  ran with it on: not ruled out for that one.
- Likely trigger (inference, not proven): the step prompt tells the agent both "Provider-native filesystem, shell, edit, and browser tools are disabled" and, a block
  before, "Save deliverable files under output/ when using native file tools"; it says several files are READ-ONLY, and that a denied write is a terminal failure. The CLI's
  own working folder is an isolated temp folder (`mlp-cli-session-*`), not the step folder.
- A "WRITE-ACCESS REALITY CHECK" guard exists on one step only (`enrich-shortlist`, added 2026-10-04): a FAILED for permissions must quote the failed write command and its error.

## Done (2026-10-05, found in the saved step prompt)

Two causes in the prompt every step agent gets (read from a saved session prompt of the same template):
- The platform's own failure rule (`guidance/templates/system/step-system-prompts.md`) gave an EXAMPLE failure: "STATUS: FAILED — cannot write the summary file: this step is read-only or this turn
  explicitly narrows writes away from that folder." Agents' failure messages ("the session's read-only filesystem restriction prevents creating `job_brief.json`") echo that sentence. The rule now
  requires quoting the exact command tried and its exact error, forbids concluding "read-only" from instructions or file descriptions, and the example is a quoted real error.
- mcpagent (`agent/isolated_output.go`) appended "Save deliverable files under output/ when using native file tools, or cd output before native shell commands" to EVERY step agent with an output dir,
  while the same prompt says "Provider-native filesystem, shell, edit, and browser tools are disabled". It is now added only when native tools are on (mcpagent `a78e6cb`, pinned).
- Not proven: that these two are the whole cause; no live run yet (the failing prompts had rotated out of the logs).

## Left

- Find where the stale "Save deliverable files under output/ when using native file tools" paragraph is generated and drop it when native tools are off.
- Make the reality-check rule platform-wide for step agents (a FAILED that blames permissions needs the failing command and its stderr), not a per-step plan edit.
- Check whether the step agent can write the step folder with the bridge shell as intended on the isolated-cwd Codex path (one real run with a trivial write).

## Register notes

[PLAT-496](plat-496.md), open: agents report "read-only" without trying to write; the platform allowed the writes each time.
