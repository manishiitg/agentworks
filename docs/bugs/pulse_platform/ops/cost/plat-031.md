[← ops / cost](index.md)

# PLAT-031 — cost ledger loses run identity across UTC midnight

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | ops |
| Area | cost |
| Summary | execution-cost persistence identity and step attribution |

| Coordination | Value |
|---|---|
| Assigned agent | `Codex` |
| Ticket state | `implemented; runtime reverify` |
| Last synchronized | `2026-08-06` |

> Claim this ticket in this file before implementation. During active work,
> update this fragment rather than the shared index; synchronize the index
> once at handoff, review, or completion.

- **Priority:** P1
- **Owner:** execution-cost persistence identity and step attribution
- **Source finding:** `HARNESS-COST-LEDGER-RUN-ATTRIBUTION`
- **Source workflow:** `Workflow/rtslatency`
- **Source Pulse run:** `schedule-cron--42eca39a_1785886230496797000`
- **Reconfirmed as:** `HARNESS-RUN-ID-ALIASING` in `Workflow/hetznerssh`
  (2026-08-06): historical cost/evaluation state under
  `iteration-0/production-server` could be presented as today's run after
  rotation.
- **Problem:** the cost writer identifies an execution by a reusable run-folder
  key inside a wall-clock-date file. When one execution crosses UTC midnight,
  its costs are split between two files that both contain
  `run_folders["iteration-0/dev"]`. The ledger also attributed a $14.7928535
  Opus charge to `execution_only:10`, although `10` is a schedule-message index
  and not a step ID in `planning/plan.json`.
- **Evidence:**
  - `costs/execution/dev/2026-08-04.json` contains
    `run_folders["iteration-0/dev"]` updated through
    `2026-08-04T23:57:51.531744Z`.
  - `costs/execution/dev/2026-08-05.json` contains the same run-folder key,
    created at `2026-08-05T00:03:46.320904Z`.
  - Both ledgers contain `execution_only:10`; the 2026-08-04 entry records one
    `claude-opus-5` call costing $14.7928535.
  - The cron fires at 05:00 Asia/Kolkata (23:30 UTC), so crossing the ledger's
    UTC date boundary is normal rather than exceptional.
- **Impact:** per-run totals cannot be reproduced reliably, adjacent executions
  that reuse the same run folder cannot be distinguished, and the largest
  charge can be assigned to a non-existent plan step. Pulse therefore cannot
  make trustworthy run-level cost or step-tier recommendations from this
  ledger shape.
- **Relationship to PLAT-009:** [PLAT-009](plat-009.md) repairs how
  `get_cost_summary` reads and merges grouped/date shards. It cannot recover an
  execution identity or valid step ID that the writer never persisted.
- **Required fix:**
  1. Generate one immutable execution/run-instance ID when the schedule starts.
  2. Persist that ID on every phase and step cost event, independent of date or
     reusable run-folder path.
  3. Attribute a cost only to a validated plan step ID or to an explicit
     non-step phase such as `schedule_message`; never overload a numeric message
     index as a step ID.
  4. Make the date files storage shards only. Query and aggregation identity
     must be the execution ID.
  5. Preserve the run-folder/group fields as searchable metadata, not identity.
- **Migration:** old date/folder-only rows remain historical evidence but
  cannot always be separated safely. Mark ambiguous historical aggregates as
  such rather than inventing execution IDs or silently assigning them to the
  nearest run.
- **Acceptance:** a run beginning before UTC midnight and completing afterward
  has one execution ID across both storage shards; querying that ID returns the
  exact total once; every step-attributed row references a real plan step; and
  two executions reusing `iteration-0/dev` remain distinguishable.
- **Required tests:** a real persistence/query round trip spanning UTC midnight,
  two consecutive executions with the same run folder, rejection or explicit
  classification of numeric schedule-message indices, and reconciliation of
  phase/model totals without double counting.

- **Implementation (2026-08-05, Claude Code, `mcp-agent-builder-go` `1bfa745d5`):**
  Writer-side fix only — this closes the identity gap the writer creates;
  it does not add the reader/query layer, which is PLAT-009's territory.

  - `ContextAwareEventBridge` mints one `ExecutionID` (uuid) per bridge
    instance in `NewContextAwareEventBridge`. A bridge instance lives for the
    whole run, so this is functionally "one ID per execution," not literally
    the scheduler's `runID` — wiring the scheduler's own `runID` through would
    have required threading a new parameter across `executeJob` →
    `executeWorkflowJob` → orchestrator construction, several layers outside
    the cost-persistence code path. The bridge-lifetime ID gives the same
    guarantee (stable across a UTC-date-shard rotation, since both shards are
    written by calls from the same live bridge) without that wider blast
    radius. Exposed via `bridge.ExecutionID()`.
  - `StepTokenData.ExecutionID` carries it from event to persister.
    `TokenUsageFile.ExecutionID` (new, `omitempty`) is stamped **sticky
    first-write** in `stampExecutionID` (`base_orchestrator_tokens.go`): the
    first call to touch a run folder's aggregate claims the ID; a later call
    with a *different* non-empty ID (a second execution reusing the same run
    folder) displaces it into `TokenUsageFile.PriorExecutionIDs` instead of
    silently overwriting it.
  - Numeric-index misattribution (fix item 3) is closed at two points: (a)
    bridge-level — `numericOnlyIdentifierPattern` reclassifies any bare-digit
    `StepID` (e.g. `"10"`) to phase `"schedule_message"` before persisting,
    regardless of which phase produced it; (b) writer-level — the
    `by_step_and_model` key builder (`stepAggregationKey`) no longer falls
    back to `"<caller phase>:<numeric Step>"` (the old `execution_only:10`
    shape) when `StepID` is empty; it now always uses `"schedule_message:<n>"`.
    Both guards are heuristic (bare-digits-only), not a lookup against the
    real `planning/plan.json` step list — no code path was found that threads
    plan step IDs into the bridge/persistence layer, and building that lookup
    was out of scope for this pass. If a plan ever legitimately used a
    bare-numeral step ID, it would be misclassified; no such ID was found in
    this codebase's step constructors (`RegularPlanStep`, `MessageSequencePlanStep`,
    etc. all use descriptive slugs in every call site inspected).
  - **Root cause of the literal `execution_only:10` in the evidence was not
    pinned down.** Every `createExecutionOnlyAgent` call site
    (`controller_execution.go`, `controller_message_sequence.go`,
    `workflow_continuation_recovery.go`) passes a real `step.GetID()` /
    `state.StepID`, and `context_aware_bridge.go`'s own numeric-index fallback
    branch requires a non-empty `StepID` before it can produce phase
    `"execution_only"` — so on the paths traced, the digits `"10"` would have
    to be the literal string value of some step's real `GetID()`, not a stray
    counter. The two guards above catch this defensively at the single
    persistence choke point regardless of which caller produced it, but the
    actual upstream producer of a bare-numeral step ID for this workflow type
    is still unidentified.
  - `PhaseTokenData` / `PhaseTokenUsageFile` (`costs/phase/token_usage.json`)
    were deliberately left untouched. That file is a lifetime-cumulative
    aggregate across every phase-only run ever, not a per-execution record —
    stamping a single execution's ID onto it would misrepresent its own
    shape. The evidence in this ticket is entirely in the execution-scope
    ledger (`costs/execution/...`), which is what this fix addresses.
  - Migration: no backfill script was written. Old rows simply have an empty
    `ExecutionID` (the field is `omitempty`), which is itself the "mark as
    ambiguous rather than invent an ID" behavior the ticket asked for — no
    separate migration pass was needed.
  - **Acceptance criteria status:**
    - "one execution ID across both storage shards" — met; proven by
      `TestStampExecutionIDGivesOneIdentityAcrossTwoIndependentDateShardFiles`
      and `TestBridgeStampsStableExecutionIDAcrossTurnsAndDiffersAcrossBridges`.
    - "two executions reusing the same run folder remain distinguishable" —
      met for *detectability* (`PriorExecutionIDs` proves two executions
      touched the folder) but **not** for full aggregate separation — the
      `ByModel`/`ByStepAndModel` sums for two executions sharing a run folder
      still merge into one running total. Splitting those would need the
      `RunFolders` map keyed by execution identity instead of bare run
      folder (fix item 4's full ask), which changes the on-disk key shape
      that PLAT-009's reader consumes — deliberately not done here to avoid
      an uncoordinated breaking change to Codex's active `runtime_reverify`
      work on PLAT-009.
    - "querying that ID returns the exact total once" — not built; no query
      API exists yet. PLAT-009 territory.
    - "every step-attributed row references a real plan step" — the numeric
      case is excluded; there is no positive validation against
      `planning/plan.json`'s actual step list, so a malformed-but-non-numeric
      ID would still pass through.
  - **Tests:** `plat031_execution_id_test.go` (pure unit tests on
    `stampExecutionID` / `stepAggregationKey`) and
    `plat031_bridge_execution_id_test.go` (through
    `ContextAwareEventBridge.HandleEvent`, reusing the existing
    `recordingTokenPersister`/`noopListener` fakes). Confirmed these fail to
    compile against the pre-fix code (stashed and re-ran), so they're
    actually pinned to the fix, not vacuously passing. Not tested: a genuine
    end-to-end round trip through `BaseOrchestrator.PersistTokenUsage`'s real
    file I/O and `time.Now()` — that function has no injectable clock, so the
    UTC-midnight proof is at the extracted-helper level instead of a true
    file-system round trip.
- **Remaining/runtime reverify:** confirm against a real `rtslatency` run
    that (a) both date shards for a midnight-crossing execution carry a
    matching `execution_id` field, and (b) no new `execution_only:<digits>`
    keys appear in fresh ledger writes.

## Tectonicus reconciliation evidence — 2026-08-05

Tectonicus independently reported `costs/phase/daily` versus
`costs/execution` disagreement of 3.1× under one opaque workflow-builder
bucket. That is not evidence that the writer-side execution-ID fix regressed:
the current ticket deliberately left the lifetime-cumulative phase ledger
unchanged. It is evidence that the reader/reporting layer must not compare the
two shapes as though they were equivalent execution totals.

PLAT-009 owns the query/aggregation work. PLAT-008 now records the shared
reconciliation acceptance boundary: a real execution must reconcile exactly
once in every view that claims to represent that execution, or the UI must
describe its scope as cumulative/non-comparable.

## Completion update — 2026-08-06 (Codex)

**Platform ID:** `PLAT-031`
**Status:** implemented; awaits one real run plus rotation for runtime
reverification.

The writer and reader are now genuinely execution-keyed; this supersedes the
earlier sticky-field-only implementation above.

- Daily cost files retain their existing date/group sharding, but their
  authoritative payload is now `executions[execution_id]` with a `token_usage`
  aggregate and `run_folder` / `archived_run_folder` metadata. New writes never
  read from `run_folders["iteration-0/..."]`, so a reused active folder cannot
  inherit old spend.
- `run_folders` remains as a legacy read-compatible projection. New readers
  use `executions` and deliberately ignore that projection when a v2 record is
  present, preventing double-counting during migration. Old rows without an ID
  remain visibly `legacy:<run-folder>` rather than being falsely split.
- Rotation now changes only `archived_run_folder` on matching execution records
  (for both execution and evaluation cost scopes). The immutable UUID and its
  totals do not move or merge when `iteration-0` becomes `iteration-N`.
- Evaluation score history now uses the same shape: every generated report has
  an `evaluation_id`, and `scores/evaluation/...` is keyed by that ID rather
  than a reusable target-run folder. Repeated evaluations no longer overwrite
  each other.
- Server cost/evaluation projections expose `execution_id` and archived path,
  so callers can show individual executions rather than attributing history to
  today's active run.

Focused regression coverage:

- `TestExecutionKeyedCostLedgerSeparatesIterationZeroReuse`
- `TestArchiveRunCostPathsUpdatesOnlyExecutionDisplayPath`
- `TestReadRunAcrossDatesUsesExecutionKeyedRecordsNotLegacyProjection`
- `TestEvaluationLedgerSeparatesRepeatedIterationZeroEvaluations`

The remaining acceptance item is runtime evidence from a production run and
subsequent rotation; no migration invents IDs for ambiguous historical data.
