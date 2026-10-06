[← platform / learnings-knowledge](index.md)

# PLAT-129 — 13 more guidance test assertions traced to specific renames and removals from `0174b6aff`/`aad50dfb0`/`f67ccc832`

| Field | Value |
|---|---|
| State | closed |
| Priority | P3 |
| Product | goals |
| Area | learnings |
| Summary | P3 — same class as PLAT-128: test/content drift, nothing |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `implemented` (13 of PLAT-128's 19-failure baseline); 6 remain, itemized below, not attempted here |
| Last synchronized | `2026-08-17` |

- **Priority:** P3 — same class as PLAT-128: test/content drift, nothing
  product-facing was broken.
- **Owner:** `cmd/server/guidance/render_all_test.go`

## Method

Same discipline as PLAT-128: for every missing phrase, `git log -S"<phrase>"
-- <template file>` to find the commit that last changed its count, then read
that commit's actual diff to confirm removal vs. rename, then confirm the
current state by **rendering** the template (`renderFromRegistry`), never by
grepping the raw file (a rendered doc can differ from its source file via
shared includes). Every claim below was rendered, not assumed.

## Findings, grouped by cause

**1. `pulse-archive` doesn't exist** (1 assertion). Deleted in `0174b6aff`
along with its Go implementation (`pulse_archive.go`, 174 lines) and its own
test file. Zero live callers. Removed from
`TestFocusedScheduledPulseReferencesStayComplete`'s map.

**2. `goal-advisor.md`'s entire pedagogical/mechanics apparatus was stripped
by `0174b6aff`** (9 test functions, ~50 assertion lines). The file went 538
lines → a fraction of that in one commit. Confirmed by rendering the current
template: every one of the ~50 phrases these tests checked — the
active-experiment lifecycle, the analysis-first-over-HTML contract, the
strategy-first-pass apparatus (`PHASE 1A`/`1B`, `data-experiment-kind`,
`NON-NEGOTIABLE STRATEGY-FIRST PASS`), the metrics-subsystem-abstention
guidance, `assumption-audit` linking, and its shared "coherent agentic
steps"/"deterministic fetcher"/"shared-context span" phrasing — is absent.

Two whole functions removed outright
(`TestGoalAdvisorPrioritizesStrategyOverHTMLFormatting`,
`TestGoalAdvisorTreatsCleanAbstentionAsStrategyEvidence`: 5/5 and 32/32
phrases confirmed absent). Everywhere else, `goal-advisor` was one entry in a
**multi-kind** map (`design-plan`, `plan-design`, `regular`,
`message-sequence`, `step-config`, `improve-report`, etc. alongside it) — only
the `goal-advisor` entry was dropped; every sibling kind in the same map was
individually confirmed still correct by rendering it, not assumed correct by
association.

**3. `builder/improve.html` dashboard markup is gone from every template on
disk** (3 test functions, ~25 assertion lines). `grep -rc
"data-pulse-section\|data-module=" cmd/server/guidance/templates` returns
nothing anywhere. This is the same root cause as PLAT-128's
`review-improve-log` finding, surfacing through a structurally different set
of tests (per-kind HTML-attribute checks rather than direct
`review-improve-log` kind rendering). `TestPulseRelatedGuidanceUsesFourPartSectionOwnership`
removed outright (8/8 kinds' phrases confirmed absent or markup-only);
`pulse-setup`'s want-block in `TestPulseRunsEveryDueReviewerAndWritesAttributedResults`
removed (4/4 absent, including its own literal `"builder/improve.html"`
check); the trailing `data-pulse-section`/`data-module` pair dropped from that
same test's `ops-review`/`strategy-auditor` loop, keeping `"Do not truncate"`,
which is real.

**4. Three genuine renames, not removals** (4 test functions, updated rather
than deleted):
- `call_generic_agent` → `run_in_background` for `strategy-auditor` and
  `review-artifact-drift` (`aad50dfb0` "stabilize pulse orchestration and
  scheduled sessions")
- `"READ-ONLY REVIEW"` → `"READ-ONLY STRATEGY AUDIT"` for `strategy-auditor`'s
  dispatch instruction, same commit. (A second, unrelated `"READ-ONLY REVIEW"`
  occurrence in `TestPulseGuidanceRequiresRuntimeAuthorityAndVisibleFreshness`
  checks the raw `docs/design/pulse-post-run-monitor-spec.md` file directly —
  confirmed that file still says it verbatim — left untouched.)
- `finding_id`/`target_key` → `"no invented identifier"` for `ops-review`,
  `strategy-auditor`, `review-artifact-drift` specifically (`f67ccc832`
  "Pulse platform hardening"), while 6 other specialists still use
  `finding_id`. `TestPulseSpecialistsReturnStructuredPacketsAndParentOwnsHTML`
  restructured to a per-kind want-list instead of one shared list, so this
  distinction — a real, deliberate design split, not drift — is expressible
  going forward rather than fought against.
- `improve-learnings`/`improve-knowledge`/`improve-database`/`improve-report`/
  `improve-evaluation` consolidated into a shared `"ENGINEERING REVIEW — <lens>
  LENS"` header, dispatched through prose ("normal Engineering/Ops background
  executor") rather than each carrying its own standalone `"READ-ONLY <X>
  HEALTH REVIEW"` title and `call_generic_agent` call.
  `TestMaintenanceImproveGuidanceIsReadOnlyForPulseFixerHandoff` updated to
  the current titles and mechanism phrase.

**5. Two stray single-phrase removals from `0174b6aff`, unrelated to the
above categories**: `design-plan`'s `"never discard findings"` and
`assumption-audit.md`'s `"Do not add an assumptions panel"` (a
dashboard-specific instruction, moot now that the dashboard it referred to is
retired). Each was the sole remaining failure in an otherwise-passing
function; both confirmed via `git log -S` against the same commit.

## Verification

Fail-before/pass-after: `git stash push -u` (all changed + new files),
re-run, `git stash pop`, re-run — 19 failures before, 6 after, across
`cmd/server`, `guidance`, `virtual-tools`, `step_based_workflow`. Zero new
failures anywhere. `TestEveryPulsePlatformTicketIsLinkedFromTheRegister`
(the register-integrity check added since PLAT-128) passes against this
ticket's own row.

## The 6 that remain — itemized, not attempted here

These are not drift; each is either a real product-architecture question or a
real scheduler bug, and guessed-in test edits would hide rather than surface
them:

1. **`TestWorkflowScheduleTrackingWindowStartSurvivesEmptySchedulerState`** and
2. **`TestRecordWorkflowSchedulePreflightFailureFailsOpenAtThreshold`** —
   both exercise the exact mechanism **PLAT-080** ("an old cron schedule with
   no durable fire-decision row restarted from 'now'") claims to have fixed,
   currently marked `implemented — runtime reverify pending`. The second
   fails with a real read/write round-trip bug: `RecordWorkflowSchedulePreflightFailure`'s
   count goes backward on the second call
   (`schedule_execution_history_test.go:191`, "failure count = 1, want 2").
   This may mean PLAT-080 regressed or was never fully fixed — worth its own
   investigation, referenced from PLAT-080 rather than duplicated here.
3. **`TestStandalonePulseReviewCommandsUsePersistedReviewerPipeline`** — the
   `call_generic_agent` → `run_in_background` rename (item 4 above) is not a
   simple word-swap here: probed, and the entire "persisted standalone-review"
   contract this test checks (`pulse_run_id`/`review_run_id` handling, SQLite
   persistence, `get_pulse_state(view="review")`) is absent for
   `ops-review`/`strategy-auditor`/`review-artifact-drift` alongside the old
   dispatch call. `run_in_background`'s actual persistence/attribution model
   is unfamiliar territory not investigated here — needs someone who knows
   what replaced it, not a guessed test edit.
4. **`TestFocusedScheduledPulseReferencesStayComplete`** and
5. **`TestStrategyAuditorGuidanceRequiresLongitudinalEvidenceAndReadOnlyHandoff`** —
   both check `pulse-review-fixer.md` for phrases (`"automatic completion
   notifications"`, `"Do not finish this Review+Fix turn until their evidence
   has been consolidated"`) that `git log -S` across **the entire repo
   history, every file** cannot find having ever existed anywhere. Unlike
   every finding above, this is not "removed" — it looks like content that
   was planned and asserted for but never actually written. A product
   decision (write it, or decide the test was aspirational and retire it),
   not a mechanical fix.
6. **`TestArtifactDriftAuditsTheSchedule`** (`schedule_drift_scope_test.go`) —
   `review-artifact-drift.md` is missing `"execute_step"`, `"validate_plan"`,
   and a drift-case phrase `"drives no plan step"`. Not yet traced to a
   commit; likely connected to the same `0174b6aff`/later restructuring given
   how much else in this file changed, but not confirmed.
