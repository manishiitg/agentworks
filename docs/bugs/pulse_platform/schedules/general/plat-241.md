[← schedules / general](index.md)

# PLAT-241 — Per-step `pre_validation_*.json` timing documents never get their embedded `run_folder` repointed after rotation

| Field | Value |
|---|---|
| State | open |
| Priority | - |
| Product | schedules |
| Area | general |
| Summary | harness_issue, severity high. |

| Coordination | Value |
|---|---|
| Assigned agent | Claude Code |
| Ticket state | `root cause confirmed live — fix design specified (corrected: overwrite in place, not additive), implementation deferred` |
| Last synchronized | `2026-08-29` |

- **Priority:** harness_issue, severity high.
- **Findings:** Twitter/social-media `PUL-8E22A469` — archived cost
  summaries are correctly distinct per iteration, but every sampled
  execution-attempt timing document under archived iterations 270-272
  encoded `run_folder=iteration-0/default`, even though those runs had
  long since rotated to their permanent names.

## Root cause, directly confirmed live (not inferred from the finding alone)

Read a real, current file:
`workspace-docs/Workflow/social-media/runs/iteration-294/default/logs/step-0-cdp-test/pre_validation_saved-script_execution_001_attempt_001.json`
(archived under `iteration-294`, dated 2026-08-27). Its own top-level
`run_folder` field reads `"iteration-0/default"` — the live slot name at
the moment the check ran, before that run's folder was later rotated to
`iteration-294`. This confirms the finding's claim is current, not stale.

The mechanism is well-understood and already has three working precedents
in this exact codebase. `rotatePairedIterationZero`
(`controller_run_manager.go`) moves `iteration-0` to a permanent
`iteration-N` name, then calls three sibling "archive" functions to
repoint each data surface that names the old slot:
`ArchiveRunCostPaths` (`token_usage_store.go`), `archiveEvaluationScoreRunFolder`
(`evaluation_score_storage.go`), and `ArchiveScheduleRunFolder`
(`run_history_archive.go` — itself filed as a fix for the *identical*
mechanism in the schedule-popup surface, whose own doc comment explains it
precisely: *"Cost and token totals are looked up per run folder, and
rotation already archives cost records... So every history row resolved to
the one folder still called iteration-0."*). All three exist because each
data surface independently hit this same class of bug at a different
layer. Per-step `pre_validation_*.json` documents are a fourth surface
that was never given the same treatment.

Because every file inside the just-moved folder was part of the SAME
atomic rename, there is no "unrecoverable history" ambiguity here (unlike
`ArchiveScheduleRunFolder`'s doc comment, which explicitly warns against
guessing at OLDER entries whose true folder is genuinely lost) — every
`pre_validation_*.json` still reading the old `from` name at the moment of
rotation is unambiguously now at `to`.

## Why this is not fixed in this session

- `get_cost_summary`, the consumer this finding's evidence actually
  exercised, already returns correct distinct totals per archived
  iteration — confirmed by the finding's own evidence, and consistent with
  `ArchiveRunCostPaths` already being correct. The concrete, tested
  behavior is not broken.
- The three existing sibling "archive" functions have thin test coverage
  (`ArchiveRunCostPaths` has one test; `archiveEvaluationScoreRunFolder`
  and `ArchiveScheduleRunFolder` have none), and this change would sit in
  the same critical, best-effort-on-failure run-rotation path — a rushed
  implementation without a proper test harness risks a subtle bug in code
  that runs on every single run rotation, a much higher blast radius than
  a typical guidance or validator fix.
- Unlike `ArchiveRunCostPaths`, `pre_validation_*.json` files are nested
  two levels deeper (`<run>/<group>/logs/<step-id>/*.json` vs. the
  siblings' flat `<root>/<group>/<file>.json`), so the existing pattern
  needs a genuinely new directory-walk shape, not a copy-paste.

## Recommended fix design (for whoever implements this)

**Correction (2026-08-29, independent review):** the first draft of this
section recommended adding a new `archived_run_folder` field alongside the
existing `run_folder`, copying `ArchiveRunCostPaths`'s convention. That
convention is right for cost/evaluation ledgers because the *original*
`RunFolder` value there is an immutable part of the record with its own
consumers, and `ArchivedRunFolder` is deliberately an addition, not a
replacement. It does not transfer here: `grep`-ing this repo for
`.RunFolder` reads on the pre_validation struct finds **no Go-side
consumer at all** — `RunFolder` on `PreValidationResult`
(`pre_validation.go:134`) is write-only from Go's perspective. Adding a
second field nobody is required to prefer would reproduce the exact bug
this ticket exists to fix, just one field over — any future or existing
consumer that reads `run_folder` (an agent, a script, a person) still gets
the stale value unless it separately knows to check
`archived_run_folder` first, and nothing enforces that.

Given there is no immutable-record constraint and no known consumer to
break, the correct fix is simpler than the cost-ledger pattern: **overwrite
`run_folder` in place** at rotation time, not add a parallel field. Add a
fourth sibling, e.g. `archivePreValidationRunFolder(ctx, from, to string)
error` in `step_based_workflow`, called from `rotatePairedIterationZero`
alongside the other three (best-effort, `Warn`-only on failure, matching
the existing three). Walk `<runsPath>/<to>/*/logs/*/pre_validation_*.json`
(group → step-id → file, one extra `ListWorkspaceFiles` level versus the
existing siblings). For each file whose top-level `run_folder` field
equals `from + "/" + <its own group>`, set it directly to
`to + "/" + <group>` and rewrite the file. Idempotent by construction — a
file already renamed to `to` simply won't match `from` on a later pass, so
no extra "already done" guard is needed the way the additive siblings need
one. Write a test harness first (none of the three existing siblings have
one worth modeling closely; a fresh fixture using the real
`ListWorkspaceFiles`/`WriteWorkspaceFile` test doubles already used
elsewhere in this package is the right starting point) before wiring it
into the live rotation path.

## Verification

Confirmed via direct inspection of a real, current archived file and the
three existing sibling implementations/call site — not inferred from the
finding text alone.

## Reverify

Not applicable — no fix shipped this session. Reverify once implemented,
by confirming a newly-rotated run's `pre_validation_*.json` files have
their `run_folder` field updated in place to the permanent iteration
name.
