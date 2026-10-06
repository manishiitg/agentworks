[← goals / steps / routing](index.md)

# PLAT-294 — No structural validation that `routing` forks resolve before other steps

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | goals |
| Area | steps/routing |
| Summary | — the review's objection to a positional "routing before any work" check is accepted and that proposal is dropped. |

| Coordination | Value |
|---|---|
| Assigned agent | Claude Code |
| Ticket state | `implemented locally (mutation-time rule, no migration); tests pass; not deployed` |
| Last synchronized | `2026-09-05` |

## 2026-09-05 — Decision and implementation: at most one routing step per plan, enforced only on new routing steps

### What the review changed

The review below is accepted: position is the wrong axis. Real, drift-reviewed
plans legitimately put the routing step at index 1–2 behind setup/classifier
steps (upwork, social-media, linkedin), and the contract explicitly allows a
prior `message_sequence` to write `route_selection.json`. No positional or
"previous step" rule was implemented.

### What the data showed instead

A census of every `routing`/`branch` step across `workspace-docs/Workflow/*`
(with `agent_configs.drift_review` from `step_config.json`) found that in every
workflow exactly one routing step is the **mode selector** — the step whose
route schedules pick via `route_selections` — and every *second* routing step
is an in-flow decision by `branch.md`'s own definition:

| workflow | mode selector (schedules select it) | second `routing` step | really a |
|---|---|---|---|
| hetznerssh | `workflow-entry-route` | `run-remediation-route`: skip→end / remediate→1 step→end | branch |
| linkedin | `step-workflow-router` | `step-post-approval-gate`: publish/redraft/draft-new/hold | branch |
| social-media | `step-run-mode-router` | `step-0-browser-router`: ok→continue / failed→end | branch |
| sheet-analysis | `route-job` | `finish-router`: both routes → `workflow-complete` | not a fork |

`plan_drift_review`'s `routing_best_practices` check passed linkedin's and
social-media's second routings and has never run on hetznerssh or
sheet-analysis (zero drift-review records), so the agentic check alone does not
hold the line. That is the case for one deterministic cardinality rule.

### The rule (owner decision)

**A plan has at most one routing step.** Routing = the mode selector; every
further fixed choice is a `branch`. Owner decision: **do not migrate existing
workflows** — what is already in a plan stays; only *new* routing steps are
checked. So the rule lives on the two mutations that can introduce a routing
step, not on plan load or plan write:

- `add_routing_step` rejects the call when the plan already has a routing step
  (top-level or orphan); nothing is written.
- `convert_routing_branch_step_type` with `target_type: "routing"` rejects while
  another routing step exists; converting *to* `branch` is always allowed.

The error names the existing routing step and both ways forward (add it as a
`branch`, or convert the existing one first). Plans with two routing steps keep
them and every other mutation keeps working on them.

**Second rule, same scope — a routing route may not point at `end`.** Owner
framing: routing is for a sub-workflow of the main work (many steps); a simple
if-condition is a branch. The census gives a clean deterministic tell for that:
every real mode selector has zero routes to `end`; every misclassified second
routing step has one (`skip_remediation → end`, `browser_failed → end`,
`route-hold → end`). A "mode" that does nothing is an if. So `add_routing_step`
and `convert_routing_branch_step_type` to routing also reject any route whose
`next_step_id` is `end`, pointing the builder at `add_branch_step`. Route-target
*existence* is not re-checked here — the plan-write graph validator
(`PLAN_GRAPH_INVALID`) already enforces it. Branch steps may route to `end`
freely. Existing routing steps with `end` routes are untouched, and
`update_routing_step` is deliberately not gated so edits to grandfathered
plans keep working.

### Files

- `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/routing_step_cardinality.go`
  — `existingRoutingStepID`, `validateSingleRoutingStepForMutation`,
  `validateRoutingRoutesStartSubWorkflows` (new).
- `planning_agent.go` — hooked into `createSingleStepAdder` (after the plan is
  read, `stepType == "routing"` only) and
  `createConvertRoutingBranchStepTypeExecutor` (`targetType == "routing"`
  only); `add_routing_step` tool description and the convert tool's
  `target_type` description state the rule.
- `interactive_workshop_manager.go` — canonical workshop prompt's fixed-choice
  sentence carries the rule (existing source-scan test still passes).
- `cmd/server/guidance/templates/system/routing.md`, `branch.md`,
  `plan-design.md` — rule documented for the builder.
- `routing_step_cardinality_test.go` (new, 7 tests): second routing step
  rejected and plan not written; orphan routing step counts; first routing
  step still accepted; a grandfathered two-routing plan still accepts branch
  steps; convert-to-routing rejected while another exists, convert-to-branch
  still allowed; routing route to `end` rejected on add; branch with an `end`
  route rejected on promotion to routing.
- `controller_branch_test.go` — the existing branch→routing conversion fixture
  now routes to real steps (the promoted step must satisfy the `end` rule).

### Verification

`go build ./...` clean (pre-existing onnxruntime linker warnings only). Full
`go test ./pkg/orchestrator/agents/workflow/step_based_workflow/` passes
(-count=1), including the 7 new tests and the existing convert/workshop-prompt
tests; `go test ./cmd/server/guidance/` (template render) passes. Not committed, not deployed; no workflow plan was modified. Deliberately
**not** done: any migration of hetznerssh/linkedin/social-media/sheet-analysis,
a load-time validator, a fall-through precompute, or a route-target-distinctness
check — all noted as possible follow-ups, none requested.

## 2026-09-05 — Code review: do not implement the proposed blanket ordering check

The absence of a routing-first validator is confirmed, but it is not sufficient evidence of a defect. The current routing contract explicitly permits an earlier agent step to classify/probe and write `route_selection.json` (`guidance/templates/system/routing.md:14`). Both proposed checks would reject that supported flow. A major fork can also be a legitimate second phase; array position alone does not distinguish it from a small branch.

Corrections to the original proposal:

- The validation file is `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/planning_management.go`, not under `cmd/server`.
- `validateNextStepIDReferences` (line 378) checks that referenced IDs exist; it does **not** impose forward-only or array-index execution ordering. It is not precedent for the recommended index check.
- The local hetznerssh fixture confirms `run-remediation-route` at index 10 of 13, after the audit/report/summary path. Its choices are `skip_remediation → end` and `remediate_now → prepare-remediation-proposal → end`. Under the documented major-route versus small-decision distinction, this is a candidate for **branch**, subject to workflow-owner intent. That is a semantic classification finding, not proof that all downstream routing is invalid.
- Define the invariant around route-specific work being gated by its route selection, permitting shared preparation and explicit later major forks. A graph check must account for explicit jumps, implicit sequential continuation, `end`, alternate entry/execute-step paths and cycles. Start with review/lint findings for ambiguous major/minor classification; do not make a speculative convention a hard plan-load failure.

No plan was rewritten and no new validator was added. Before implementation, revise the acceptance criteria and include a valid classifier → routing fixture, an intentional later major fork, a small branch, and accidental fall-through into an unselected sibling. The original array-index-first recommendation below is superseded by this review.

## 2026-09-05 — Routing steps can be scattered through a plan with no lint catching it

### Observation

`routing` and `branch` share one executor and one field validator
(`validateRoutingStepTyped`, called from `validateLoadedPlanStepWithOptions`,
`planning_management.go:200-204`), but by design they mean different things:
`routing` is documented as "the 'route' concept: a major, self-contained
sub-workflow fork," while `branch` is "a small in-flow next-step decision"
(`agent_go/cmd/server/guidance/templates/system/branch.md`). Nothing enforces
that shape at the plan level — a `routing` step can appear anywhere in the
step list, including after other steps have already executed.

Concrete example: `workspace-docs/Workflow/hetznerssh/planning/plan.json` has
`routing` steps at position 0 (`workflow-entry-route`, the entry fork — as
intended) **and** at position 10 of 13 (`run-remediation-route`), after six
`regular`/`message_sequence` steps (audit, evidence correlation, report
generation, Slack summary) have already run. That second fork is legitimate
in isolation (it's a real "do we remediate at all" decision), but nothing
distinguishes "an intentional second major fork placed here on purpose" from
"a `routing` step that should have been a `branch`, or should have been
hoisted earlier" — the plan validator is silent either way.

### Why this matters

The `routing`/`branch` split only holds as a useful signal for plan readers
(and for the builder LLM choosing between them) if it's actually enforced.
Today it's convention-only, so plans can silently drift into using `routing`
where `branch` was meant, or into interleaving major forks with the very
steps they were supposed to gate — the exact ambiguity this pair of step
types exists to avoid.

### Proposed fix (not implemented)

Add a plan-level structural check at the existing validation seam,
`validateLoadedPlanStructureWithOptions`
(`planning_management.go:236`), which already runs on every plan load and
write and already does one whole-plan check (`validateNextStepIDReferences`,
called at line 240) after per-step validation. A new
`validateRoutingOrdering(plan)` would flag any `routing`-type step that is
reachable only after a non-routing step has already executed — i.e. major
forks must resolve before the plan commits to a branch of work; `branch`
remains unrestricted since it's meant to be used anywhere downstream.

Two implementation options, open design question:

1. **Array-index check** (simple, matches how `validateNextStepIDReferences`
   already treats step order): flag any `routing` step whose index is greater
   than any non-routing step's index that precedes it in the same reachable
   path.
2. **Graph-reachability check** (more correct, more work): walk from the
   entry step; flag a `routing` step if reaching it requires passing through
   a `branch`/`regular`/`message_sequence` step first, regardless of raw
   array position.

Recommend starting with (1) since it reuses the existing validator's
approach and catches the hetznerssh-shaped case, with (2) as a follow-up if
false positives/negatives show up in real plans.

### Verification (not started)

No test/build/deploy evidence yet — this is a design proposal from a chat
discussion, not a landed change. hetznerssh's current plan should be the
first regression fixture once the check is implemented (either accepted as
intentional-second-fork, or flagged and reworked).

## Register notes

[PLAT-294](plat-294.md) — the review's objection to a positional "routing before any work" check is accepted and that proposal is dropped. A census of every workflow showed exactly one `routing` step per plan is the mode selector schedules pick via `route_selections`, and every second one (hetznerssh, linkedin, social-media, sheet-analysis) is a `branch` by `branch.md`'s own definition; the agentic `routing_best_practices` check passed two of them and never ran on the others. Implemented as two deterministic rules on the two mutations that can introduce a routing step (`add_routing_step`, `convert_routing_branch_step_type` to routing): at most one routing step per plan, and no routing route straight to `end` (routing = a sub-workflow of many steps; a mode that does nothing is a simple if-condition → branch). **No load-time check and no migration by owner decision**: existing plans keep their routing steps as they are. Seven new tests pass locally with the full package; not committed, not deployed.
