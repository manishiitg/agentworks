[← goals / evaluation](index.md)

# PLAT-189 — `message_sequence` never proactively showed its `validation_schema`; authored schemas tend to be over-specified

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | goals |
| Area | evaluation |
| Summary | P2 — a real prompt-contract inconsistency across step types |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `fixed` (proactive surfacing) + `open` (schema-bloat guidance, judgment-only, no code enforcement) |
| Last synchronized | `2026-08-27` |

- **Priority:** P2 — a real prompt-contract inconsistency across step types
  (not a runtime bug: the step still worked, it just only learned its
  output contract reactively, after failing once) plus a recurring
  authoring quality issue the user has observed directly ("many times we
  overdo the schema with what's not required").
- **Owner:** `pkg/orchestrator/agents/workflow/step_based_workflow/controller_message_sequence.go`
  for the code fix; `cmd/server/guidance/templates/system/prompt-engineering.md`
  and `cmd/server/guidance/templates/review/ops-review.md` for the guidance.
- **Related:** surfaced while investigating a user challenge to guidance
  written for [prompt-engineering.md's initial version] — the user pushed
  back on "name the exact shape in the description," correctly suspecting
  the schema was "sent already" for at least some step types.

## Finding 1 — proactive schema surfacing was inconsistent across step types (fixed)

Traced all three agentic step types' prompt-construction code directly (not
assumed from convention):

- **`regular` steps** (`execution_only_agent.go:124`,
  `{{if and .ValidationSchema (ne .IsScriptedMode "true")}}`) — schema
  rendered into the system prompt on the first attempt.
- **`todo_task` steps** (`todo_task_orchestrator_agent.go:78`,
  `{{if .ValidationSchema}}`) — same, with an explicit code comment
  confirming the intent: "so the orchestrator knows which output files must
  exist on the first attempt — otherwise it only learns the requirements via
  ValidationFeedback after a failed attempt."
- **`message_sequence` steps** — NOT rendered proactively.
  `controller_message_sequence.go`'s opening-turn construction
  (`session.LastRuntimeContext`) carried only the raw step description; the
  schema was enforced as an appended final-validation queue item and shown
  to the agent only reactively, after a failed pre-validation attempt
  (`formatMessageSequencePrevalidationFeedback`).

`message_sequence` is the plan-design default for essentially all
conversational/judgment work, so this was the step type most likely to hit
the gap in practice.

**Fix:** `formatMessageSequenceValidationSchema` + `appendMessageSequenceValidationSchema`
render the schema into the opening turn the same way `regular`/`todo_task`
already did, joined onto the existing opening instruction (reentry message
or step description) with a blank line. Covers both the standalone-run and
first-route-call paths. Unit tests:
`TestFormatMessageSequenceValidationSchemaNilReturnsEmpty`,
`TestFormatMessageSequenceValidationSchemaRendersRequiredFiles`,
`TestAppendMessageSequenceValidationSchemaNoSchemaLeavesContextUnchanged`,
`TestAppendMessageSequenceValidationSchemaWithNoExistingContextReturnsSchemaOnly`,
`TestAppendMessageSequenceValidationSchemaJoinsAfterOpeningInstruction` — all
pass; full package build/test verified, two pre-existing unrelated failures
(`TestRunInBackgroundPassesBuilderSkillSnapshotToBothAgentKinds`,
`TestWorkshopPromptShellExamplesUseAbsolutePaths`,
`TestDesignPlanGuidanceSupportsReadOnlyPulseChecklist`) confirmed present on
the baseline before this change too, not introduced by it. Live reverify
pending.

## Finding 2 — authored `validation_schema`s tend to be over-specified (guidance only, open)

Independent of Finding 1: because the schema now renders into the prompt on
every attempt for every step type, its size is a live prompt cost, not just
an authoring/retry artifact — and the user has observed the recurring
pattern directly: schemas often check more than what's actually load-bearing
("not exhaustive... many times we overdo the schema with what's not
required").

**What shipped this pass:** guidance only, no code enforcement —
deliberately, since "is this check load-bearing" is a judgment call a raw
count can't proxy for (a 3-check schema can be all bloat; a 15-check schema
can be all load-bearing for a genuinely multi-file contract), and
[[feedback_metric_thresholds_data_driven]] argues against inventing a
numeric threshold with no run data to calibrate it.

- `prompt-engineering.md` gained "Let `validation_schema` name the shape —
  don't restate it in prose" (ties to Finding 1: the description no longer
  needs to spell out the object's keys once a schema exists) and "Keep
  `validation_schema` light — not exhaustive" (each check should answer
  "what real failure does this catch"; cut it if the honest answer is "none,
  it's just thorough"). The self-check section gained a matching question.
- `ops-review.md`'s Prompt-contract health section gained a third judgment
  question ("Over-specified schema") alongside the existing wrong-home/
  not-concise pair, plus an instruction to lightly `jq`-scan
  `planning/plan.json` for outsized check counts even on steps whose
  description didn't cross the size-based triage boundary — since
  `get_plan_prompt_health` has no schema-size signal at all today.
  `engineering-review.md` inherits this automatically; it already treats
  `ops-review.md` as its canonical operations checklist, no separate edit
  needed.

**What did NOT ship:** any deterministic detector for schema bloat (check
count, JSON size) in `get_plan_prompt_health`/`prompt_health.go`. Left as a
pure agentic judgment call for `/ops-review` and Pulse's `technical_review`
to catch during plan review — this ticket's state stays `open` on that half
because the guidance is unverified against a real over-specified schema in
the wild; revisit if reviews keep missing it in practice, at which point a
cheap raw-count triage signal (mirroring `Over5K`/`Over10K`/`Over20K`) might
be worth adding purely as a "look closer here" trigger, not a verdict.

## Verification

- `go build ./...` clean (both `agent_go` and `cmd/server` packages).
- New unit tests listed above, all passing.
- Full `step_based_workflow` and `cmd/server` package test suites run; only
  pre-existing, unrelated failures present (confirmed via `git stash` on a
  clean baseline).
- Not yet live-verified: no real workflow run exercised a `message_sequence`
  step with a `validation_schema` end-to-end since the fix landed.
