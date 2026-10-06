[← goals / step-execution](index.md)

# PLAT-259 — Split routing's overloaded semantics: `routing` becomes the "route" (major fork) concept, new `branch` step type for small in-flow decisions

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | goals |
| Area | step-execution |
| Summary | now extends routes-as-sub-workflows to Notify, Activity, and the existing Daily Action report source from PLAT-279. |

| Coordination | Value |
|---|---|
| Assigned agent | Claude Code |
| Ticket state | `frontend per-route reporting implemented across Execution Logs, Costs, and Evaluation` — the canonical workshop prompt and every named reference doc offer `branch` as the fixed-choice alternative to `routing`; Execution Logs retains the executed type; generic plan APIs accept branch steps; the atomic routing/branch conversion tool publishes the changelog `reason` its executor requires; and every step reached through a run's actually-selected route is now tagged and filterable by route in Execution Logs, Costs, and Evaluation. A successful live manual rerun of `/verify-branch-step`/`/migrate-routing-to-branch` remains the one open item |
| Last synchronized | `2026-09-05` |

## 2026-09-05 — Route-specific Daily Actions and Pulse summaries

**Codex follow-up implemented locally; not committed/deployed.** The user asked
to extend routes-as-sub-workflows to the daily actions already produced by
Notify and to Pulse summaries. Related: [PLAT-264](../../platform/integrations/plat-264.md)) (one durable
notification source) and [PLAT-279](../../platform/frontend-chat/plat-279.md)) (Daily Actions uses that source).

The old `summary_route` string was only a label. Activity kept one latest Run
and one latest Pulse summary per workflow, and fifty records per kind could
let a busy route erase the quieter route's last status. Pulse could also
inherit the schedule's execution route despite reviewing a different scope.

The follow-up adds:

- `notify_user(summary_routes=[...])`: one digest with a typed entry for each
  route actually represented by its evidence. Identity is the exact
  `(routing_step_id, route_id)` pair; label/title/status/message and optional
  fields/sections describe that route. Small `branch` choices stay internal.
  Shared work stays in the existing top-level content. The backend renders
  the same route facts into external message text and escaped Gmail HTML.
- The existing `org_dashboard_notifications` table gets an additive
  `route_summaries_json` and `summary_text` columns. The original `message`
  stays complete for old reports; new views render the shared lead and typed
  route entries once. A digest remains one row. Retention preserves
  fifty updates per kind and route, as well as the workflow stream, and the
  API exposes latest Run/Pulse records per route independently of recent-list
  limits. No copied history or workflow-owned activity table is introduced.
- Activity displays each route's latest Run/Pulse status and keeps a quieter
  route's blocker visible after another route succeeds. Details show individual
  route content. Legacy labels are retained separately without guessing their
  router identities. Missing Pulse coverage stays missing.
- Pulse no longer inherits execution-route scope merely from schedule
  metadata. Finalizer guidance uses actual route-scoped review evidence and
  forbids allocating aggregate costs to routes without ledger attribution.
- Workflow contract `1.0.40` supplies a bounded migration for existing
  notification instructions and Daily Action / Recent Activity reports. It
  preserves custom activity sources and legacy/absent-column fallback,
  validates edits, adds no steps or notification calls, changes no recipients,
  and performs no workflow execution or notification send during migration.

Verification covers mixed-route digest persistence, identical route IDs under
separate routers, independent route/kind retention beyond fifty busy updates,
legacy-label separation, shared Pulse scope, session-to-provider delivery,
escaped Gmail rendering, Activity route details, and upgrade sequencing.
TypeScript compilation passes. A full guidance run also exposed and corrected
one stale PLAT-163 test phrase about mandatory review of any measured miss.

Remaining acceptance after deployment: one real multi-route run and one Pulse
pass must produce truthful route entries; existing report upgrades must render
both new route data and old/absent scope without inventing coverage. No live
workflow records, notification recipients, or issue-resolution counts changed
as part of this implementation.

- **Type:** implemented platform feature. Originally filed at the
  user's explicit request immediately after the design converged, to record
  the full negotiated shape before implementation starts.
- **Origin:** the user opened a design discussion, not a bug report: today's
  single `routing` step is used interchangeably for two genuinely different
  needs — a small in-flow "if this then step A else step B" decision, and
  what should really be a major, self-contained sub-workflow fork. The user
  named this directly: *"route is like a major sub workflow... branch is a
  small decision to choose next step... right now we use route vs branch
  interchangeably, but both should be different."*

## Current state (confirmed by code reading, not assumption)

`RoutingPlanStep` (`agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/planning_agent.go:393`,
`step.Type == "routing"`) is a deterministic N-way switch:

- Never runs an agent/LLM itself. `description` must be empty — the
  executor (`controller_routing.go:44-46`) hard-errors if it isn't, forcing
  any judgment/probe logic into a *prior* step that writes
  `route_selection.json`.
- Route selection (`controller_routing_deterministic.go`,
  `resolveDeterministicRoutingSelection`) checks, in order: a pre-seeded
  `route_selection.json` in the step's own execution folder (settable via
  `run_full_workflow`'s `route_selections` param before the run starts),
  `route_source_file`, a `context_dependencies` entry literally named
  `route_selection.json`, then `default_route_id`; otherwise a hard error.
- No sub-workflow concept exists anywhere in the plan schema — every step
  type lives in one flat `steps[]` array regardless of which route led to
  it. Nothing stops two routes' downstream paths from converging back onto
  a shared step. No per-route eval breakdown, no per-route reporting
  grouping.

## Design (agreed with the user across this session)

**Keep `routing`/`RoutingPlanStep` completely unchanged in code.** Zero
migration, zero backward-compat risk to any existing stored `plan.json` —
nothing about the struct, the JSON type tag, or the executor changes.
Reinterpret it *conceptually*, going forward, as the **route** concept
(major fork): "routing" already means choosing a route linguistically, so
no new type is needed for this half. Existing plans that used `routing` for
small decisions keep executing exactly as before — nothing here is a
retroactive runtime requirement.

**Add one new step type, `branch`**, for the small in-flow next-step
decision — same shape/executor as `routing`'s current behavior, just a
distinct type tag so guidance, reporting, and eval tooling can tell the two
apart going forward.

**Route-specific properties are agentic/guidance-level, not Go-enforced.**
No shared downstream steps between sibling routes, and always getting an
eval breakdown when the workflow has one, are deliberately *not* hard
runtime validators — the user was explicit about this ("not enforceable in
golang code"), wanting a planning-agent self-check (the same pattern as the
`step-description.md` guidance skill from PLAT-255) rather than a rigid
validator that could block a legitimate exception. The user also confirmed
the "no shared steps" rule already conceptually exists today and simply
isn't consistently followed — this ticket's guidance work is what's
supposed to close that gap, not a new mechanism.

**Reporting is the one real, buildable feature, not a best practice.**
Once `routing` is reliably treated as "route" going forward, execution-log
reporting can mechanically group by `step.Type == "routing"` into a
top-level tab per route — this is deterministic UI work, not something to
nudge the planning agent toward.

### Alternatives considered and rejected

- **New `route` type + rename existing `routing` → `branch`.** Rejected:
  forces a choice between a one-time migration of every stored `plan.json`
  or a permanent `routing`-as-alias-of-`branch` shim, for no benefit —
  `routing`'s existing name already fits the "route" concept better than
  it fits "branch."
- **New `route` type added alongside unchanged `routing`.** Rejected:
  `routing` and `route` are lexically almost identical (one is a substring
  of the other), reintroducing the exact route/branch mixup that started
  this discussion — a planning agent or a human skimming `plan.json` could
  easily conflate them.

## Phase A — implemented: the `branch` step type

`branch` is a real, working step type now, functionally identical to
`routing` (unchanged, per the design above).

**Backend** (`agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/`):
- `StepTypeBranch` constant + `BranchPlanStep` struct (`planning_agent.go`)
  — a **distinct Go struct**, not `RoutingPlanStep` reused with a `Type`
  differentiator (resolves the first open question below): the codebase's
  dispatch pattern is pervasive type-assertion (`step.(*RoutingPlanStep)`),
  which a single reused struct with two type tags would have fought against
  everywhere. One deliberate field-name difference: `branch_question`, not
  `routing_question` — `RoutingPlanStep` itself stays untouched.
- Extracted a new `routeSwitchStep` interface (`GetRoutes`,
  `GetDefaultRouteID`, `GetRouteSourceFile`, `GetRoutingQuestionText`,
  `SetSelectedRouteID`, `SetRoutingResponse`, plus `PlanStepInterface`)
  implemented by both `RoutingPlanStep` and `BranchPlanStep`. The entire
  executor (`executeRoutingStep` in `controller_routing.go`,
  `resolveDeterministicRoutingSelection` and its helpers in
  `controller_routing_deterministic.go`, ~540 lines total) now operates on
  this interface instead of the concrete `*RoutingPlanStep` type — one
  execution code path for both step types, nothing duplicated.
- Wired into every polymorphic step-type switch (JSON parse/unmarshal ×3,
  `updateToolForStepType`, `createSingleStepAdder`'s validation switch,
  `isRoutingStep` in `controller_execution.go` — broadened to recognize
  both types since every existing caller wanted identical treatment for
  branch: no learnings, routes through the same executor — and the
  legacy-description pre-flight guard in `planning_exports.go`).
- New `add_branch_step`/`update_branch_step` tools (**distinct tools**,
  resolves the second open question below — matches the codebase's
  established one-tool-pair-per-step-type convention rather than a shared
  tool with a `kind` parameter): own JSON schema
  (`getAddBranchStepSchema`/`getUpdateBranchStepSchema`), own executors
  (`createAddBranchStepExecutor` delegates to the same generic
  `createSingleStepAdder("branch", ...)` routing already uses;
  `createUpdateBranchStepExecutor` mirrors `createUpdateRoutingStepExecutor`
  exactly), registered next to the routing tools. Added to both allow-lists
  (`interactive_workshop_manager.go`'s Workshop-mode tool list,
  `planning_management.go`'s tool-name group checks) and the toolset
  invariant test's tracking list (`cmd/server/toolset_invariant_test.go`) —
  the exact registered-but-unreachable gap PLAT-258 phase 3 caught for
  `record_plan_drift_review` doesn't recur here.
- New `cmd/server/guidance/templates/system/branch.md` (mirrors
  `routing.md`'s structure: selection contract, single mode, structure,
  convergence, anti-patterns) plus a short reinterpretation note at the top
  of `routing.md` and updated coverage in `plan-design.md`'s step-type
  decision guide (Step 6 + the type enumeration) so the planning agent
  actually learns branch exists at the point it chooses a step type, not
  only inside the reference doc. New `branch` entry in `guidance.go`'s
  `referenceKinds` registry.

**Frontend** (`frontend/src`): no manual step-type picker exists anywhere —
steps are authored by the planning agent — so this is purely
recognize/render/exclude, no new UI. `stepConfigMatching.ts`'s `PlanStep`
union gained `BranchPlanStep` + `isBranchStep`/`isRouteSwitchStep` guards;
`usePlanToFlow.ts`'s node/edge building, route-target collection, and
layout sizing now handle branch alongside routing; `nodes/index.ts`/
`edges/index.ts` register `branch: RoutingStepNode`/`branch: RoutingEdge`
(reused as-is, not forked, since branch renders identically to routing);
`WorkflowCanvas.tsx`'s node inspector and `LearningsView.tsx`'s
learnings-eligible-step filter both extended; `ExecutionLogsPopup.tsx`'s
three step-type label/description/badge helpers gained a `branch` case
with its own cyan badge, distinct from routing's indigo, so the two are
visually distinguishable in Execution Logs (the one surface where telling
them apart actually matters for this phase — reporting them into separate
top-level tabs is still future work, see below).

## Phase B — implemented: route best-practices in `plan_drift_review`

Asked directly whether the two route checks should be a deterministic Go
graph-traversal or a Group 3 judgment check (the reviewer LLM reasons about
it); the user chose **judgment check**, matching their original design
preference ("not enforceable in golang code") — no new Go algorithm.

- `PlanDriftCandidate` (`plan_drift_candidates.go`) gained a `StepType`
  field, precomputed by reading `planning/plan.json` (via the existing
  `PlanningResponse`/its `UnmarshalJSON`) alongside the already-read
  `step_config.json` — best-effort, tolerates a missing/unparseable
  plan.json by leaving it empty rather than failing the scan. Lets the
  reviewer turn know which candidates are routing steps without an extra
  lookup, matching the module's existing "precomputed evidence" philosophy.
- `plan-drift-review.md` gained two new Group 3 checks, explicitly gated to
  `step_type == "routing"` only (never `branch` — branch is deliberately
  the small in-flow decision, these don't apply to it):
  `route_structural_isolation` (trace each route's `next_step_id` chain;
  legitimate convergence at a shared step, per `routing.md`'s documented
  pattern, is not drift — an *interior* step reachable from more than one
  sibling route is) and `route_eval_pairing` (if the workflow has an
  `evaluation_plan.json` at all, is there an eval step whose
  `applies_to_routes` covers this routing step — a real, already-documented
  field in `evaluation-plan.md`, not something invented for this).
- `guidance.go`'s `plan-drift-review` reference-kind description updated to
  mention the two new checks.

## Explicitly not done (still open)

- **Frontend per-route top-level reporting tabs.** Not built — Execution
  Logs currently show routing and branch steps with distinct badges/labels,
  but neither gets a dedicated tab. This is real, buildable UI work once
  `routing` reliably means "route" going forward; scoping it is a separate
  pass.

## Open questions for the implementation phase

The structural questions from the design phase are resolved by Phase A
(distinct `BranchPlanStep` struct; distinct `add_branch_step`/
`update_branch_step` tools) and Phase B (judgment check, not deterministic
Go, per explicit user choice) above. Remaining, for the one not-yet-done
item:

- Frontend: where exactly the per-route top-level tab lives in the
  Execution Logs / reporting surfaces, and how it interacts with the
  existing step-summary view for plans with zero `routing` steps.

## Verification

- `go build ./agent_go/... ./workspace/...` clean, `gofmt -l` clean.
- `go test ./pkg/orchestrator/agents/workflow/step_based_workflow/...`,
  `./cmd/server/guidance/...`, and `./cmd/server/ -run TestToolSetInvariants`
  all pass — including 7 new tests in `controller_branch_test.go`
  (validation accept/reject parity with routing, `isRoutingStep` recognizing
  both types, JSON marshal always setting `type: "branch"`, `parseStepFromJSON`
  round-tripping a real branch payload) and one existing test updated for the
  new "regular, human_input, todo_task, routing, branch, or message_sequence"
  error text (`TestPlanningResponseRejectsLegacyConditionalStep`). Full
  `cmd/server` suite has one pre-existing, unrelated failure
  (`TestEveryPulsePlatformTicketIsLinkedFromTheRegister`, missing
  `plat-248.md`/`plat-249.md` from a concurrent session — confirmed
  reproducible with this ticket's changes fully stashed out). Phase B added
  2 more tests to `plan_drift_candidates_test.go`
  (`TestCollectPlanDriftCandidatesPopulatesStepTypeFromPlanJSON`,
  `TestCollectPlanDriftCandidatesToleratesMissingPlanJSON`) — both pass;
  `cmd/server/guidance` package has one further pre-existing, unrelated
  failure (`TestStrategyAuditorGuidanceRequiresLongitudinalEvidenceAndReadOnlyHandoff`
  — a text-mismatch bug in a concurrent session's own `pulse-gate.md`/test
  pair, confirmed reproducible with Phase B's changes stashed out too).
- `cd frontend && npx tsc --noEmit -p . && npm run build` clean;
  `npx vitest run` has 2 pre-existing failures unrelated to this change
  (`sessionRestore.productFallback.test.ts`, video-studio mock-argument
  drift — confirmed reproducible with this ticket's changes fully stashed
  out).
- See "Corrective audit — 2026-08-30" below for the fuller re-verification
  after the independent review's findings were fixed, including the new
  end-to-end `TestBranchStepEndToEndLifecycle` regression test and two UI
  mislabeling fixes (`RoutingStepNode.tsx`'s untitled-step fallback label,
  `WorkflowCanvas.tsx`'s inspector section title — both previously said
  "Routing" unconditionally even for a branch step).

## Reverify

Phase A: ask the planning agent to add a `branch` step to a real workflow
plan via `add_branch_step`, confirm it appears correctly on the canvas
(indigo routing / cyan branch badges distinguishable in Execution Logs),
executes via `run_full_workflow` exactly like a routing step would, and
that
`read_skill(skills=[{"name":"builder-reference","path":"references/branch.md"}])`
returns the new guidance. **Now unblocked** — the independent review's
findings below were all fixed in the corrective audit and are covered by
the new automated regression test; this manual live reverify against a real
workflow run has not been executed yet and remains the one open item before
closing phase A entirely.

Phase B: trigger `plan_drift_review` on a workflow with a routing step
whose two routes share an interior step (not a legitimate shared
convergence point), and separately on one with an `evaluation_plan.json`
that has zero `applies_to_routes` coverage for a real routing step, confirm
the reviewer turn actually judges and raises both as findings via
`record_pulse_finding` rather than skipping them. Phase B does not depend
on the phase A gaps below (`plan_drift_review` reads plan.json directly; it
does not go through the runtime switches the review found broken).

The one remaining "explicitly not done" item above (frontend per-route
reporting tabs) stays open follow-up work, not covered by either reverify.

## Independent review — 2026-08-30

The distinct `branch` type and shared `routeSwitchStep` executor are a sound
design, but phase A is **not usable end to end** and must not be described as
implemented or working yet.

1. **Canonical plan validation rejects every branch step.**
   `validateLoadedPlanStepWithOptions` handles regular/evaluation, human,
   message-sequence, routing, and todo steps, but has no `BranchPlanStep`
   case. It therefore returns `unsupported step type *BranchPlanStep during
   loaded plan validation`. Every persisted plan write passes through this
   validator after a JSON round trip, so `add_branch_step` cannot reliably
   persist a branch and an existing branch plan cannot reliably reload.
2. **The runtime switch audit is incomplete.** At minimum:
   - `populateRuntimeFields` has no branch case and returns `unknown step
     type`;
   - `ApplyStepConfigFromFile` does not attach per-step or global config to a
     branch;
   - `getAgentConfigs` has no branch case;
   - post-execution navigation extracts the selected route's `next_step_id`
     only from concrete `*RoutingPlanStep`, leaving a branch target empty;
   - `validateNextStepIDReferences` validates routing targets but not branch
     targets, so dangling branch edges can escape graph validation;
   - route-scoped validation, nested sub-agent identity normalization, and
     other routing-specific switches still omit branch.

The corrective implementation should audit every concrete
`*RoutingPlanStep` switch and use `routeSwitchStep` wherever routing and
branch share semantics. Acceptance needs one end-to-end regression test that
adds a branch, persists the plan, reloads it, applies its config, executes a
selected route, and verifies navigation reaches that route's target. The
seven existing tests cover parsing and isolated parity only; they do not
exercise that lifecycle. Reporting tabs and plan-drift guidance should wait
until this runtime contract passes.

## Corrective audit — 2026-08-30, all findings above resolved

Methodology change: the original phase A audit grepped for the
`StepTypeRouting` string constant, which misses every call site that
type-asserts the concrete `*RoutingPlanStep` type directly. The corrective
pass instead ran `grep -rln "RoutingPlanStep" --include="*.go" .` to list
every file, then read every matching line in each file to decide whether
`*BranchPlanStep` needed an identical case. This found every gap the review
named, plus one it implied but didn't name directly:

- `validateLoadedPlanStepWithOptions` (`planning_management.go`) — now
  `case *RoutingPlanStep, *BranchPlanStep:` (the critical fix; branch steps
  can now persist and reload).
- `populateRuntimeFields` / `populateStepRuntimeFields`
  (`planning_management.go`) — branch case added, config now applies.
- `ApplyStepConfigFromFile` (`step_config.go`, both `matchedConfig` and
  `overrides` switches) — branch case added.
- `getAgentConfigs` (`controller_execution.go`) — branch case added.
- Post-execution navigation — the inline "find next step based on selected
  route" block in the main execution loop was extracted into a standalone
  `nextStepIDForSelectedRoute(step, selectedRouteID) string` function
  (specifically so it has direct test coverage), type-asserting the shared
  `routeSwitchStep` interface instead of the concrete `*RoutingPlanStep`.
- `validateNextStepIDReferences` (`planning_management.go`) — branch case
  added to the `next_step_id` graph walk, so dangling branch route targets
  are now caught the same as dangling routing targets.
- Route-scoped validation (`planning_exports.go`:
  `routeScopedValidationSteps`, `inferValidationRoute`,
  `routeSegmentEndIndex`) — all three switched from `*RoutingPlanStep` type
  assertions to the `routeSwitchStep` interface.
- Nested sub-agent identity normalization (`setStepIdentity`,
  `planning_agent.go`) — **found during this audit, not named explicitly by
  the review's bullet list, but implied by its general instruction.** Used
  to stamp a `todo_task` predefined route's `sub_agent_step` with the
  route's ID/name; had no `*BranchPlanStep` case, so a branch step nested as
  a sub-agent step would hit `unsupported sub_agent_step type` and error.
  Fixed; covered by `TestSetStepIdentityAcceptsBranchStep`.
- `updateValidationSchemaOnStep`, `cloneStepWithDelegationOverrides`
  (`controller_todo_task.go`) — branch cases added.
- `mergePartialStepUpdate` and the field-change-tracking section of
  `updateSingleStep` (`planning_agent.go`) — **a bug this audit introduced
  in its own earlier phase A work, caught during the systematic re-check,
  not flagged by the review's text.** `mergePartialStepUpdate` had no
  `*BranchPlanStep` case at all, so `update_branch_step`'s executor would
  silently return the step unchanged for any field update (hit the
  `default: return existingStep` fallback). Added the missing case, plus a
  `BranchQuestion` field on `PartialPlanStep` (which didn't exist), plus
  changelog old-value tracking for `branch_question` and switched the
  `Routes`/`DefaultRouteID`/`RouteSourceFile` old-value lookups to the
  shared `routeSwitchStep` interface so they work for both types.
- `validateRoutingStepTyped` (`planning_management.go`) — extended to
  type-assert `routeSwitchStep` instead of `*RoutingPlanStep`, so the
  `validatePlanStepIDsAtPath` call path (a separate, pre-existing validator
  from `validateRoutingStepFieldsTyped`) now validates branch steps too.

**End-to-end regression test added** (`controller_branch_test.go`,
`TestBranchStepEndToEndLifecycle`), the acceptance bar the review set: adds
a branch step with two routes to a plan, validates it via
`validateLoadedPlanStructure` (would previously error with `unsupported
step type`), round-trips it through marshal/unmarshal and re-validates,
applies `step_config.json` via `populateRuntimeFields`/`getAgentConfigs`
(would previously silently no-op), and confirms
`nextStepIDForSelectedRoute` resolves each route to its correct
`next_step_id` (would previously return empty, stalling execution).
`TestBranchStepDanglingNextStepIDCaughtByValidation` covers the
`validateNextStepIDReferences` fix separately.

All 11 tests in `controller_branch_test.go` pass; full
`step_based_workflow` package suite passes; `go build ./...`, `gofmt -l`
clean; `go vet` has only pre-existing, unrelated issues (confirmed
reproducible with this ticket's changes stashed out —
`generate_text_llm_tool_p0_reviews_test.go`'s missing `agentreview` module,
`message_sequence_stop_test.go`'s context-leak vet warning,
`scheduler_test.go`'s unreachable-code vet warning); `cmd/server` full
suite has the two pre-existing failures already on record
(`TestEveryPulsePlatformTicketIsLinkedFromTheRegister`,
`TestStrategyAuditorGuidanceRequiresLongitudinalEvidenceAndReadOnlyHandoff`)
plus one more confirmed pre-existing and unrelated the same way
(`TestUpgradeDedicatedPulseSchedulePromptShape` — a periodic-pulse-review
handoff-prompt text mismatch, unrelated to routing/branch); frontend
`tsc --noEmit` and `npm run build` both clean.

Phase A's reverify (below) is now unblocked.

## Second independent review — 2026-08-30

Reviewed current `origin/main` after the corrective audit. The core phase A
implementation is substantially improved: `BranchPlanStep` now participates
in canonical validation, config application, graph-reference validation,
selected-route navigation, nested identity normalization, update handling,
canvas rendering, and the shared execution dispatch. Focused branch/routing,
plan-drift, guidance, tool-invariant, and frontend TypeScript checks pass.

PLAT-259 nevertheless remains open for four reasons:

1. **Phase B does not invalidate earlier drift reviews.** Adding
   `route_structural_isolation` and `route_eval_pairing` changes the required
   review contract, but a routing step with an existing
   `drift_review.needs_review=false` remains clean. There is no review-contract
   version or one-time migration that marks these existing routing reviews
   stale, so workflows reviewed before phase B can silently miss both checks.
2. **Nested routing candidates lose their type.** Candidate discovery's raw
   walk includes nested todo-route `sub_agent_step` IDs, but
   `stepTypeByID` is populated only from top-level `PlanningResponse.Steps`.
   A nested routing step therefore reaches the reviewer with an empty
   `step_type`, and the two routing-only checks are skipped.
3. **`route_eval_pairing` proves only one reference, not complete route
   coverage.** Its current contract passes when any evaluation step references
   the routing-step ID. An eval covering one route out of five therefore
   satisfies the check. Acceptance requires comparing the union of referenced
   `route_ids` with every route declared by the routing step, with explicit
   handling for intentionally route-agnostic evaluation.
4. **The named end-to-end test does not execute the branch.**
   `TestBranchStepEndToEndLifecycle` validates parse/round-trip, loaded-plan
   validation, config population, and the isolated
   `nextStepIDForSelectedRoute` helper, but never calls the real
   `executeRoutingStep`/controller execution path. A live workflow execution
   remains explicitly unverified, and frontend per-route reporting tabs are
   still explicitly unbuilt.

Required closure work: add a route-review contract/version invalidation or
migration; populate candidate type recursively; require complete route eval
coverage; exercise a real branch execution through the controller (plus the
already-listed live reverify); and implement or explicitly split the
per-route reporting UI into a linked follow-up ticket before calling this
feature complete.

## Second independent review — resolved 2026-08-30

Addressed all four required-closure items:

1. **Drift-review contract versioning.** Added `StepDriftReview.ContractVersion`
   and a package constant `planDriftReviewContractVersion` (currently 2 —
   1 was the original phase 1-6 check set, 2 adds phase B's two routing
   checks) in `plan_drift_candidates.go`. `CollectPlanDriftCandidates`'s
   due-ness check now also fires when `ContractVersion <
   planDriftReviewContractVersion` (zero/missing counts as always-stale,
   correctly — no review recorded before the field existed could have run
   checks a later version added), alongside the existing
   `NeedsReview`/nil-record conditions. `createRecordPlanDriftReviewExecutor`
   stamps the current version on every completed review. This is a global
   version bump (every step gets one re-review pass, not only routing
   steps) — simpler than trying to scope invalidation to just the step
   types a given version's new checks apply to, and safe: a one-time extra
   review of an already-clean non-routing step is a false-positive-safe
   over-inclusion, not a correctness problem. Covered by
   `TestCollectPlanDriftCandidatesReflagsStaleContractVersion` and an
   assertion added to `TestRecordPlanDriftReviewExecutorWritesNewRecord`.
2. **Nested routing candidates keep their type.** `CollectPlanDriftCandidates`
   built `stepTypeByID` from only top-level `plan.Steps`, while candidate
   discovery (`planStepIDsFromPlanJSON`) already recurses into a
   `todo_task`'s `predefined_routes[].sub_agent_step`. Added
   `collectStepTypesByID`, the typed equivalent of that same recursion
   (mirrors `collectKnownWorkflowStepIDs` in `planning_exports.go`), so a
   nested routing/branch step's `StepType` is populated the same as a
   top-level one. Covered by
   `TestCollectPlanDriftCandidatesPopulatesStepTypeForNestedRoutingStep`.
3. **`route_eval_pairing` requires full coverage.** Rewrote the check's
   guidance in `plan-drift-review.md`: instead of passing on any single
   `applies_to_routes` reference, the reviewer must union every matching
   eval step's `route_ids` and compare against the routing step's full
   declared route set, naming any missing `route_id` as the finding. Two
   explicit judgment carve-outs documented: an unscoped eval step (no
   `applies_to_routes` at all) can count as covering routes with no
   route-specific eval only if it genuinely evaluates something the
   routing decision doesn't affect, not if it only exercises whichever
   branch a run happened to take; and a route landing on a trivial no-op
   destination may legitimately have nothing worth evaluating, judged, not
   assumed. This stays a judgment check (per the original phase B design
   choice), not new Go code — deterministic route-coverage math would
   still need to answer "is this eval step's scope actually about this
   route" (as e.g. #4 above found is the point of the human judgment).
4. **Real branch execution through the controller.**
   `TestExecuteRoutingStepRunsRealBranchExecution` (`controller_branch_test.go`)
   drives a `*BranchPlanStep` through the real `executeRoutingStep`, using
   the same `httptest.NewServer` + `WorkspaceClient` mocking pattern as
   `base_orchestrator_workspace_test.go`: every read answers "not found" so
   resolution falls through to `default_route_id` (the same path a plain
   `*RoutingPlanStep` already exercises live when no `route_selection.json`
   exists yet), folder creation and the `routing-evaluation.json` write are
   mocked to succeed. Asserts the real executor returns the correct
   selected route, persists `SetSelectedRouteID`/`SetRoutingResponse` onto
   the branch step struct, and that feeding its output into
   `nextStepIDForSelectedRoute` (the same call the main execution loop
   makes) resolves to the route's real `next_step_id`. This is the first
   test — for routing OR branch — that calls `executeRoutingStep` at all
   (confirmed via `grep -rln "executeRoutingStep(" --include="*_test.go"`
   returning empty before this).

All four fixes verified: `go build ./...`, `gofmt -l`, `go vet` clean (only
the same pre-existing unrelated `vet`/test findings already on record);
`go test ./pkg/orchestrator/agents/workflow/step_based_workflow/...` and
`./cmd/server/guidance/...` green;
`TestStrategyAuditorGuidanceRequiresLongitudinalEvidenceAndReadOnlyHandoff`,
previously a recorded pre-existing failure, is now fixed upstream by a
concurrent session and passes.

Still open, unchanged from before this review: the live manual reverify
against a real workflow run, and the frontend per-route reporting tabs.

## Third independent review — 2026-08-30

Reviewed current `origin/main` at `eceb4c187`. The core branch implementation
is now substantially sound: the shared executor, canonical plan validation,
route selection, config application, navigation, nested type discovery,
drift-review contract versioning, route-eval coverage guidance, and frontend
plan/canvas typing are present. The full `step_based_workflow` package tests,
focused server/tool-invariant/branch/routing/Plan Drift tests, and frontend
TypeScript check pass.

PLAT-259 is still not complete for three newly confirmed integration reasons:

1. **The canonical builder prompt still defeats the semantic split.**
   `interactive_workshop_manager.go`'s primary planning instruction omits
   `branch` from the step-type list and explicitly tells the agent to use
   deterministic `routing` for fixed branch choices. The same stale direction
   remains in `plan-design.md`, `planning-steps.md`, `message-sequence.md`,
   `scripted.md`, and parts of `workflow-tools.md`. Normal plan creation can
   therefore keep producing routing steps for the small decisions PLAT-259
   introduced `branch` to represent. Update every canonical entry point and
   add a guidance-contract test that requires both types and their distinction.
2. **Branch execution is mislabeled as routing in Execution Logs.**
   `workflow.go` converts every `routing-evaluation.json` artifact into an
   orchestration entry with hardcoded `"type": "routing"`. A real branch run
   consequently renders a routing-colored inner event and “Routing question”
   even though the owning step header is a Branch. Carry the actual plan-step
   type into the record (or derive it from plan metadata) and add a branch
   execution-log response test.
3. **The plan add/update HTTP API rejects branch steps.** The frontend's
   `PlanStep` union and `usePlanData.addStep`/`updateStep` APIs accept a branch,
   but `handleAddStep` only unmarshals `regular` and `todo_task`, while
   `updateStepInPlan` likewise has no `BranchPlanStep` case. Builder-native
   `add_branch_step`/`update_branch_step` work, but the platform's other plan
   mutation path is internally inconsistent and returns `Unknown step type`
   or `unknown step type`. Support branch in both handlers (and test it), or
   narrow/remove the exposed generic API contract.

The two already-declared open items also remain: frontend per-route reporting
tabs and a live manual branch run against a real workflow. No runtime regression
was found in the shared branch executor itself.

## Third independent review — resolved 2026-08-30

Addressed all three findings:

1. **Canonical builder prompt now offers `branch`.** The prompt block
   inside `interactive_workshop_manager.go` (`## Planning steps`, the text
   the Builder agent reads every workshop turn, distinct from the
   `references/*.md` deep-dive docs) previously told the agent to use
   deterministic `routing` for every fixed branch choice and omitted
   `branch` from its step-types list entirely. Rewrote the guidance to
   offer both explicitly (`branch` for a small in-flow decision, `routing`
   for a major sub-workflow fork) and added `branch` to the step-types
   list and the per-step-deep-dive doc list. Extended the same fix to
   every other canonical entry point the review named:
   `plan-design.md`, `planning-steps.md`, `message-sequence.md`,
   `scripted.md`, and `workflow-tools.md` (which was also missing
   `add_branch_step`/`update_branch_step` from its tool lists entirely).
   Two new regression tests lock this in:
   `TestCanonicalWorkshopPromptOffersBranchForFixedChoices`
   (`controller_branch_test.go`, source-scans
   `interactive_workshop_manager.go`, mirroring the existing
   `TestRunInBackgroundPassesBuilderSkillSnapshotToBothAgentKinds`
   pattern) and `TestCanonicalPlanningDocsDistinguishBranchFromRouting`
   (`cmd/server/guidance/branch_step_type_distinction_test.go`, renders
   each of the five docs via `RenderSystemDoc` and asserts both `branch`
   and `routing` are documented as real step-type options, not just used
   as the English verb). Both confirmed to fail against the pre-fix files.
2. **Execution Logs now reports a branch run's real type.**
   `handleGetExecutionLogs` (`cmd/server/workflow.go`) hardcoded
   `"type": "routing"` on every `routing-evaluation.json`-derived
   orchestration entry — the artifact the shared `executeRoutingStep`
   writes identically for either step type. Now looks up the owning
   step's real type from `stepMetadata` (already populated from
   `plan.json` for other purposes in the same handler), falling back to
   `"routing"` only if metadata is missing. The frontend
   (`ExecutionLogsPopup.tsx`) previously only rendered this block for
   `log.type === 'routing'` — extended every check to include `'branch'`
   too, with its own cyan styling (matching the existing step-header
   badge convention) and a "Branch question" label instead of "Routing
   question", so a branch entry doesn't just stop rendering.
   `TestHandleGetExecutionLogsReportsBranchStepTypeNotRouting`
   (`workflow_execution_logs_test.go`) covers the backend fix; confirmed
   to fail against the pre-fix handler.
3. **The generic plan add/update HTTP API now accepts branch steps.**
   `handleAddStep` and `updateStepInPlan` (`cmd/server/workflow.go`) —
   the platform's generic plan-mutation endpoints distinct from the
   Builder-native `add_branch_step`/`update_branch_step` tools — had no
   `BranchPlanStep` case in either switch and returned "Unknown step
   type"/"unknown step type" for a branch payload, even though the
   frontend's `PlanStep` union already accepts one. Added a `case
   "branch":` to `handleAddStep` (full unmarshal, matching `regular`'s
   shape) and a `case *BranchPlanStep:` to `updateStepInPlan` (common
   fields only — title/description/context_dependencies/context_output —
   matching exactly what this generic path already offers every other
   type; route-specific fields like `routes`/`branch_question` stay the
   exclusive job of the Builder-native `update_branch_step` tool's
   privileged, validated write path, not this legacy generic one).
   `TestHandleAddStepAcceptsBranchStep` and
   `TestUpdateStepInPlanAcceptsBranchStep`
   (`workflow_branch_step_api_test.go`) cover both; both confirmed to
   fail against the pre-fix handlers.

All three fixes verified: `go build ./...`, `gofmt -l` clean; `go test
./pkg/orchestrator/agents/workflow/step_based_workflow/...`,
`./cmd/server/...`, and `./cmd/server/guidance/...` green (only the
pre-existing, unrelated `virtual-tools` missing-module failure remains —
confirmed reproducible with this ticket's changes fully stashed out, same
as prior rounds); `cd frontend && npx tsc --noEmit -p . && npm run build`
clean.

Still open, unchanged: frontend per-route reporting tabs, and the live
manual reverify against a real workflow run.

## Temporary operator diagnostic commands — added 2026-08-30

At the user's explicit request, to actually close the live-manual-reverify
item against a real workflow instead of leaving it perpetually open. Both
are deliberately **temporary** — not permanent workflow-maintenance
flows — and should be removed (their `allKinds` entry in
`cmd/server/guidance/guidance.go`, their template in
`cmd/server/guidance/templates/review/`, and their frontend command in
`frontend/src/commands/builtin-commands.tsx`) once the operator has used
them to confirm branch works in a real workflow.

- **`/verify-branch-step`** — adds (or reuses) a real branch step, runs it
  via `run_full_workflow`, and checks Execution Logs reports it as
  `"branch"` (not `"routing"`), that it executed deterministically with no
  agent turn, and that navigation reached the selected route's declared
  `next_step_id`. Cleans up any step it created for the test.
- **`/migrate-routing-to-branch`** — the user's actual ask ("convert the
  existing workflow as per best practices"): reclassifies existing
  `routing` steps in the current plan as `branch` where they're really the
  small in-flow decision PLAT-259 introduced `branch` for, and — for any
  `routing` step that legitimately stays `routing` — applies the same
  `route_structural_isolation`/`route_eval_pairing` judgment checks
  `plan_drift_review`'s phase B already added, filing
  `record_pulse_finding` for real violations instead of only reporting
  them in chat.

Neither command has been run against a real workflow by the operator yet —
that run is exactly what will finally close the "live manual reverify"
open item above.

## Two more findings, fixed 2026-08-30

1. **[P2] Execution Logs could mislabel historical runs.** The handler
   derived a routing/branch entry's `type` from the CURRENT plan.json, not
   from what the run artifact recorded at execution time. After
   `convert_routing_branch_step_type` reclassifies a step (or, previously,
   after the flawed delete-and-recreate procedure below), an older run
   that actually executed as `routing` would render as `branch` in
   Execution Logs, or vice versa. Fixed by persisting `step_type` into
   `routing-evaluation.json` itself at execution time
   (`executeRoutingStep`, `controller_routing.go`) and having
   `handleGetExecutionLogs` prefer that recorded value over the live
   plan.json lookup, falling back to the plan.json lookup only for an
   artifact written before this field existed. Covered by
   `TestHandleGetExecutionLogsPrefersPersistedStepTypeOverCurrentPlan`
   (confirmed to fail against the pre-fix handler) and an assertion added
   to `TestExecuteRoutingStepRunsRealBranchExecution` proving the artifact
   write includes `step_type`.
2. **[P2] `/migrate-routing-to-branch`'s original procedure did not
   preserve history as claimed.** Its guidance said restoring a step's
   original id after converting it kept `step_config.json`/drift-review
   history continuous, but the procedure's own `delete_plan_steps` call
   removed the old id's `step_config.json` row before the id could ever be
   reused — the claimed continuity was false. Rather than patch the
   guidance to be more careful about a delete-then-recreate dance, built
   the purpose-built atomic tool the finding recommended:
   `convert_routing_branch_step_type(existing_step_id, target_type)`
   (`planning_agent.go`) relabels a step's type **in place** — the step's
   `id` (and therefore its `step_config.json` row) is never touched at
   all, because routing and branch already share the exact same
   deterministic-switch shape (only the question field's name differs).
   Registered alongside `add_branch_step`/`update_branch_step`, with the
   same validation/changelog/drift-review-invalidation contract every
   other plan-mod tool follows. `/migrate-routing-to-branch`'s guidance
   rewritten to use this tool instead of the flawed procedure. Covered by
   `TestConvertRoutingBranchStepTypeFromRoutingToBranch`,
   `TestConvertRoutingBranchStepTypeFromBranchToRouting`, and
   `TestConvertRoutingBranchStepTypeRejectsNoOpConversion`
   (`controller_branch_test.go`) — the first explicitly asserts
   `step_config.json` is never written to during a conversion.

Note: `convert_routing_branch_step_type` is a genuinely useful, permanent
tool (unlike the two temporary slash commands above) — it stays even after
`/verify-branch-step`/`/migrate-routing-to-branch` are eventually removed.

All fixes verified: `go build ./...`, `gofmt -l` clean; `go test
./pkg/orchestrator/agents/workflow/step_based_workflow/...`,
`./cmd/server/...`, `./cmd/server/guidance/...` green (only the same
pre-existing unrelated `virtual-tools` failure).

## Live migration contract failure fixed (2026-08-30)

The first real `/migrate-routing-to-branch` run against `build-in-public`
proved the new atomic conversion tool could not be called: its executor used
the standard `requireReason(args)` changelog guard, but
`getConvertRoutingBranchStepTypeSchema` exposed only `existing_step_id` and
`target_type`. Omitting `reason` therefore failed in the handler, while
supplying it was rejected as an unknown field by the bridge. The run correctly
made no workflow changes and recorded the platform finding `PUL-4E3281CD`.

Fixed the contract at its source:

- the tool schema now publishes and requires `reason`;
- the registered tool description and both relevant guidance documents show
  the complete three-argument contract;
- `TestConvertRoutingBranchStepTypeSchemaPublishesReason` locks the published
  schema to the executor requirement, preventing another handler/schema split.

The `build-in-public` plan remains unchanged by this platform repair. Rerun
the migration after deploying/restarting the updated server; the four proposed
conversions still require a fresh classification pass, and the existing
route-evaluation finding `PUL-4D0912A3` remains separate workflow work.

## Plan-canvas distinction added (2026-08-30)

Routing and branch already persisted and executed as separate types, but the
plan canvas reused an indistinguishable node presentation. The shared node now
renders routing with a teal `Route` icon and visible `Route` label, while a
branch renders with a violet `GitBranch` icon and visible `Branch` label.
Execution-mode iconography remains available separately, so making the step
type visible does not discard scripted/agentic/direct execution information.
The production frontend build passes.

## Frontend per-route reporting — implemented (2026-08-31)

Closes the "Explicitly not done" item above: the plan schema and executor
already knew a step could belong to a route (Phase A), but nothing computed
which route a *downstream* step actually belonged to for a *given run*, so
none of Execution Logs, Costs, or Evaluation could group or filter by route.
Route membership is a per-run fact (the same plan can take a different route
on a different run), not a static plan property, so it has to be derived
from that run's actual `selected_route_id`, not merely the routes the plan
declares.

**Backend** (`agent_go/cmd/server/workflow.go`), computed once and reused by
all three surfaces rather than re-derived per surface:

- `collectSelectedRoutes` — a lightweight pre-pass over the same step-log
  folder tree `processLogsFolder` walks (mirrors its wrapper-folder
  recursion), reading each routing/branch step's `routing-evaluation.json`
  for this run's real `selected_route_id`. Has to run before any
  `stepsLogs` entries are created, since those seed from `stepMetadata` on
  first creation and downstream folders can be visited before or after
  their owning routing step's folder.
- `computeRouteMembership` — for each routing/branch step with a recorded
  selection, walks from the selected route's `next_step_id` to a
  convergence point (generalizes `routeSegmentEndIndex` from
  `planning_exports.go`, which only ever handled the first routing step for
  validation pruning), tagging every step in that segment with
  `route_id`/`route_name`/`route_kind:"routing"`/`route_step_id`/
  `route_step_title`. Bounded on the other side by the nearest sibling
  route's own entry point (`siblingBoundary`), so an untaken sibling route's
  steps are never swept in by the walk — caught by a regression test before
  shipping (the untaken route's step was initially tagged with the taken
  route's info). Per the user's explicit choice, a step where sibling routes
  legitimately converge is tagged with whichever route this run actually
  took, not left unlabeled.
- `/api/workflow/logs` now returns these fields per step. Kept field-
  distinct from the pre-existing, unrelated `route_id`/`parent_step_id`
  pair (the `todo_task` orchestrator's `predefined_routes[].sub_agent_step`
  case) via `route_kind`, per the user's explicit choice to keep the two
  mechanisms visually separate rather than unify them.
- New regression test `TestHandleGetExecutionLogsTagsDownstreamStepsWithSelectedRoute`
  (`workflow_execution_logs_test.go`): a routing step with two routes,
  only one selected; asserts the selected route's downstream step is
  tagged and the untaken sibling's step is not.

**Frontend**, all three reusing the same Execution Logs response instead of
each re-deriving route logic:

- `ExecutionLogsPopup.tsx` — route filter pill bar (teal, matching the
  canvas's `Route` icon convention above) above the step list, plus a
  per-step "↳ route name" chip, visually distinct from the existing
  orchestrator sub-agent-route chip.
- `CostsPopup.tsx` — the same route filter on the "By Step" cost breakdown
  view (it already fetched Execution Logs for step-title lookup, so route
  data came for free), a route badge per step row, and the Total row
  becomes a route-scoped subtotal when a filter is active rather than
  silently showing the whole run's total.
- `EvaluationPopup.tsx` — surfaces `applies_to_routes`, a real field on
  `EvaluationStep` (`route_eval_pairing`'s own field, Phase B) that reached
  the frontend's raw `evaluation_plan` JSON but was previously parsed out
  and discarded by `parseEvaluationPlanDetails`. Now kept, and
  cross-referenced against each run's actual route selections (fetched
  lazily from Execution Logs when a report expands) to show, per eval
  step, which route(s) it's scoped to and whether that route was actually
  taken this run — plus a route filter bar mirroring the other two
  surfaces.

**Verification:** `GOWORK=off go build ./...` and `gofmt -l` clean; full
`cmd/server` package suite passes (only the same pre-existing, unrelated
`virtual-tools` and `scheduler_test.go` findings already on record for this
ticket remain). `cd frontend && npm run build` (`tsc -b && vite build`)
clean.

Landed at `b87736573` on `main`.

Still open, unchanged: the live manual reverify against a real workflow run
(the two temporary operator commands from the section above), which this
work did not touch.

### 2026-09-05 — Wider plan routes and click-to-trace (local)

The plan canvas now gives each primary route segment its own column (six columns for Build in Public instead of packing six routes into three). Lane gaps increase from 144 to 240 pixels. Branch handoffs remain connected; routing edges fan out above the route bodies. Steps follow their actual successor order, including LinkedIn observation → evidence → draft, instead of their order in the plan file. Shared joins and variable-height standalone rows no longer share overlapping positions. Layout version `2.8-wide-route-lanes` invalidates stale saved positions.

Routing and branch choices now have keyboard-accessible trace buttons. The clicked choice follows the rendered downstream graph through nested decisions, handoffs, shared joins and cycles, while other nodes fade to 14% and edges/labels to 8%. Context-dependency edges do not expand a trace. A loop back to the source router cannot activate its other choices. Trace state is local to the canvas and workspace; it never writes runtime `selected_route_id` or changes the plan. Click the same choice, Show all, the canvas background, or Escape to clear. A Fit plan to view control supports navigating the wider map.

Verified against the local Build in Public plan in the in-app browser: route switching, LinkedIn membership, CDP Unavailable branch membership, click-to-toggle and Escape. Regression coverage checks separate router identities, end routes, missing choices, shared joins, nested handoffs/cycles, immutable runtime selection, button semantics, six distinct lanes, topology ordering and join separation. This is a local implementation; no deployment or workflow execution was performed.

Follow-up: clicking a colored routing/branch line or its numbered badge now toggles the same route trace. Exact source handles resolve the router/route pair; ordinary connections do not activate a route. A wider invisible line hit area makes selection easier, and badge buttons support Enter/Space. Verified locally: line activation, repeat-click clearing, switching via a faded route line, badge activation and Enter clearing. Six focused tests and the TypeScript build passed.

## Register notes

[PLAT-259](plat-259.md) now extends routes-as-sub-workflows to
Notify, Activity, and the existing Daily Action report source from
[PLAT-279](../../platform/frontend-chat/plat-279.md). One digest carries independent route
outcomes; history preserves quiet routes, and Pulse scope comes from actual
review evidence. Contract `1.0.40` supplies the bounded existing-report upgrade.
Implemented/tested locally; commit, deployment and producing-run verification
remain. No live issue-resolution counts changed.
