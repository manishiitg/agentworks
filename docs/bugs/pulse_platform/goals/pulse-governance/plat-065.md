[← goals / pulse-governance](index.md)

# PLAT-065 — Gate recorded a due module, and nothing ever resolved it

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | goals |
| Area | pulse-governance |
| Summary | P1 — the dropped finding was a real P0 (public content correctness bug) |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `open` — detection shipped, proximate cause isolated, precise fix pending a safety call |
| Last synchronized | `2026-08-10` |

- **Priority:** P1 — the dropped finding was a real P0 (public content correctness bug)
- **Owner:** scheduler Pulse orchestration (`runPostRunMonitor`, `scheduler.go`)
- **Found on:** social-media, 2026-08-10, session `schedule-manual--5227790a_1786294126526391000`

## What happened

A social-media Pulse Gate pass correctly identified a real, high-severity issue and then nothing ever acted on it. Reconstructed precisely from logs and `pulse_module_state`:

1. The session ran the full workflow (posting steps) then a Gate turn, all as one continuous native Claude Code session spanning **4h13m** (confirmed from the terminal-stream close log: `duration="4h13m19.553s"`).
2. Gate correctly chose `mode=discovery` — its own reasoning was sound: *"the retained backlog (115 open concerns) doesn't explain what this run surfaced, so a drain-only pass would have missed it."*
3. Gate recorded `workflow_review: due` at `2026-08-09T21:01:29Z` (02:31 IST), citing three real triggers, headlined by: *"NEW P0 — two graders disagree on the same content. The evaluation hard-failed 3 replies for misdescribing AgentWorks against the canonical product note, while the in-run remediation step passed all 10 of those same items on that same check... the content is already public."*
4. **The session ended 42 seconds later.** No Review+Fix pass, no fix, no artifacts written. `pulse_module_state.last_result` for `workflow_review` is empty string — no Review+Fix pass has ever run against this due decision.
5. It sat unactioned for **6+ hours** until a human happened to ask about it.

## Why this is a platform gap, not a one-off

The architecture has **no safety net for this by design**. `pulse-gate.md` is explicit: *"Gate must not launch reviewers... Stop after recording the complete worklist."* The Fixer contract says *"There is no scheduler-launched residual or recovery Fixer."* Both are correct on their own terms — the intended design is that the **same session** continues past Gate into Review+Fix as one continuous handoff. When that continuation doesn't happen, nothing else is watching, and the drop is silent by construction.

**Confirmed not universal**: the same night, a cron-triggered upwork run (`schedule-cron--d4b6c12f...`) completed its own Gate→Review+Fix cycle normally — `pulse_module_state.last_result = "changed"` with a real repair description. So this is not a blanket failure of `runPostRunMonitor`; something about *this* run diverged.

**Confirmed recurring, not a one-off**: running the new detector (below) against the current state found **4 more stranded decisions**, one **374 hours old** (instagram, `learning_health`, since 2026-07-25) and one **83 hours old** (tectonicusadaytrading, `workflow_review`). This has been happening silently for at least two weeks.

## Investigation update — proximate cause isolated (2026-08-10)

The earlier theory in this section ("`runPostRunMonitor` was never entered — zero `[PULSE]` log lines") is **wrong** and is superseded by direct DB evidence found afterward. Correcting it here rather than deleting it, since it shaped (and ruled out) real hypotheses along the way.

**Direct proof the function *was* entered and ran to its stop point.** `pulse_final_command_state` for this exact `pulse_run_id` has three rows, all written in the same second:

```
backup / publish / notify → status: "failed"
reason: "Pulse stopped after gate failed while its agent turn was still live;
         refusing to overlap another message in the same conversation"
recorded_at: 2026-08-09T21:02:11Z   (42s after Gate recorded "due" at 21:01:29Z)
```

That reason string is emitted from exactly one call site: `abortIfTurnStillBusy` (`scheduler.go:2233-2242`), which builds the message `"Pulse stopped after %s failed while its agent turn was still live; refusing to overlap another message in the same conversation"` with `st.label` (here, `"gate"`) and immediately calls `finalizeUnresolvedPulseFinalCommands(..., "failed", reason)` — which is exactly why backup/publish/notify all show `failed` with the identical reason and timestamp: one call stamped all three at once, then the function returned.

**The exact mechanism** (`scheduler.go:2244-2283`, the Gate attempt loop):

```go
for attempt := 1; attempt <= 2; attempt++ {
    result := runStep(gateStep)
    if abortIfInterrupted(gateStep, result) { return }
    if abortIfTurnStillBusy(gateStep, result) { return }        // <-- fires here
    if result.outcome == postRunMonitorStepCompleted {
        ...validatePulseGateCompletion recovery...
    } else if err := validatePulseGateCompletion(...); err == nil {
        // recovers a failed/timed-out step IF the durable worklist is complete
        ...
    }
    ...
}
```

`abortIfTurnStillBusy` triggers whenever `result.outcome != postRunMonitorStepCompleted && sessionIsBusy(sessionID)` (`pulseStepFailureMustStopBeforeNextTurn`, line 2364). Gate's own tool call, `record_pulse_worklist`, **had already durably succeeded** — that's the only reason `pulse_module_state.workflow_review` shows a `due` decision with real reasoning at all. But the step's reported *outcome* was not `postRunMonitorStepCompleted`, and the session's live status still read `busy` in that same moment.

**This is an ordering bug, not a missing code path.** The function already contains exactly the right recovery logic for "the step reported failure, but the durable worklist is complete anyway" — twice (lines 2263-2271 and 2278-2283). But `abortIfTurnStillBusy` is checked *first*, at line 2252, and returns immediately when it fires. The recovery logic that would have found the completed worklist and continued into the due-check → Review+Fix → Finalize sequence never got a chance to run, because the function had already exited.

**Confirmed not universal, same as before**: upwork's same-night cron run completed its Gate→Review+Fix cycle with no busy-abort. Whatever caused `sessionIsBusy` to still read `true` 42 seconds after Gate's tool call succeeded is specific to this run (plausibly related to its unusually long 4h13m continuous session — more time for a genuine race between the tool call durably committing and the platform's turn-completion/UI-status signal catching up), not a standing bug in every Gate pass.

**What's still open**: *why* the session was still reported busy at that instant. The server log that covered this window has since rotated out, so it can't be reconstructed after the fact. **Diagnostic logging shipped (2026-08-10):** `abortIfTurnStillBusy` (`scheduler.go:2233`) now logs `step`, `outcome`, `err`, `sessionBusy`, and whether the Gate worklist was durably complete at that exact moment, before it aborts. The next occurrence will show definitively whether the recovery logic would have found a complete worklist (confirming the reorder fix is safe) or not (meaning something else is going on). Build and the existing 22-failure baseline both verified unaffected.

**The fix candidate, and the one open safety question before shipping it**: reorder so the Gate step's durable-worklist recovery check runs *before* `abortIfTurnStillBusy`, not after — i.e., attempt recovery first, and only invoke the busy-abort guard if recovery does *not* find a complete worklist. This directly closes the gap this ticket found. The caveat: `abortIfTurnStillBusy` exists specifically to prevent sending a second message into a session whose turn hasn't actually wound down — a real overlap there could corrupt the conversation, which is presumably why the guard was written to fire unconditionally in the first place. Recovering and immediately sending the next turn while `sessionIsBusy` is still `true` risks reintroducing that exact class of bug (this session already fixed several turn-completion races this cycle — PLAT-054's idle watchdog, the Codex structured-adapter turn-completion hang, the cold-turn MCP tool-connect race — so this codebase has been burned by naive fixes here before). The safer version of this fix likely needs a short bounded wait for `sessionIsBusy` to clear before sending the next step, not an instant reorder-and-send. Not implemented this session for that reason — needs the diagnostic data above to design safely rather than guessing at overlap-safety semantics.

## Fix shipped now: detection (`scripts/pulse_health.py --section stranded`)

Root cause isn't isolated, but the actual harm — a real finding sitting silently for hours to weeks — is fully preventable without fixing the scheduler bug first. New `report_stranded_gate_decisions`:

- Reads `pulse_module_state` per workflow (new field captured in `scan_workflow`).
- Flags any module where `last_gate_decision='due'` and `last_result` is empty, stale beyond a configurable threshold (`--stranded-hours`, default 2h — long enough that a legitimately in-progress review isn't false-flagged).
- Immediately found the social-media case plus 4 pre-existing ones going back to July 25, none previously noticed.

This closes the actual gap (silent for hours/weeks → visible on the next diagnostic run) independent of whatever eventually turns out to cause the drop.

## Acceptance

- **Detection (done):** `scripts/pulse_health.py --section stranded` surfaces every stranded due-decision workflow-wide.
- **Proximate cause (isolated 2026-08-10):** `abortIfTurnStillBusy` fires and returns before the Gate step's durable-worklist recovery check ever runs, whenever the step's reported outcome isn't `postRunMonitorStepCompleted` and `sessionIsBusy` is still `true` — even when the worklist was, in fact, already committed durably.
- **Underlying cause (still open):** why `sessionIsBusy` read `true` 42 seconds after Gate's tool call had already succeeded, for this run only. Needs a diagnostic log line at the `abortIfTurnStillBusy` call site (`scheduler.go:2252`) capturing outcome/err/runtime-phase, caught on its next live occurrence — the log window covering this incident has already rotated out.
- **Fix (designed, not shipped):** recover-before-abort reordering for the Gate step, gated on resolving whether it's safe to send the next turn immediately once `sessionIsBusy` clears, or whether it needs a short bounded wait first. Do not ship the reorder without deciding that — an unconditional reorder that sends the next message while the turn may genuinely still be live risks a real conversation-overlap bug, a class this codebase has already had to fix multiple times.
- **The 5 currently-stranded decisions** (social-media/workflow_review, tectonicusadaytrading/workflow_review, and 3 retired-module-name instagram rows) need manual re-triggering or a fresh Gate pass — do not silently reset them; each represents real, unresolved evidence.
