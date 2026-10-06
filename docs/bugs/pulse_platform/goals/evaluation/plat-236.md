[← goals / evaluation](index.md)

# PLAT-236 — A validation_schema could pair a non-string `value_type` with a `pattern`, which no value can ever satisfy

| Field | Value |
|---|---|
| State | closed |
| Priority | - |
| Product | goals |
| Area | evaluation |
| Summary | harness_issue (write-time guardrail gap), severity high on |

| Coordination | Value |
|---|---|
| Assigned agent | Claude Code |
| Ticket state | `implemented; update_evaluation_plan gap closed; DB-check coverage added; runtime reverify` |
| Last synchronized | `2026-08-29` |

- **Priority:** harness_issue (write-time guardrail gap), severity high on
  the findings.
- **Findings:** Twitter/social-media `PUL-08AC60BB`, `PUL-ED75E920` — both
  describe the identical mechanism: `$.reach_snapshot_table_updated`'s
  validation check had both `value_type=boolean` and a string-only
  `pattern` (`^true$`), so a real boolean `true` failed the pattern check
  ("Pattern validation only applies to strings") while coercing the value
  to a string to satisfy the pattern failed the type check instead — no
  JSON value could ever pass.

## The underlying finding was already resolved by the workflow itself

Both findings were first seen `2026-08-25T03:34–44Z`. The workflow's own
`planning/changelog/changelog-2026-08-27-15-24-56.json` shows an
`update_validation_schema` call at `2026-08-27T15:24:56Z`, reason:
*"PUL-155B3EBD: remove only the string-only `^true$` pattern from the
boolean `reach_snapshot_table_updated` check; preserve boolean typing and
every companion file/DB guard."* The live `planning/plan.json` today has
only `{"path":"$.reach_snapshot_table_updated","must_exist":true,
"value_type":"boolean"}` — no `pattern`. Same "real when filed, fixed by
the workflow's own subsequent config edit, never re-verified in the
findings" shape as PLAT-228/PLAT-230.

That fix is workflow-authored config content (`validation_schema` edited
via `update_validation_schema`), not this repo's code — the correct owner
for the original bug is the workflow's own Pulse Fixer, which already
acted. What remained a genuine platform gap: nothing in this repo's schema
write path would have caught the contradiction before it reached a live
step, and nothing stops the same shape from being authored again.

## Fix

Added `validateValueTypePatternCompatibility`, alongside the three existing
write-time schema validators (`validateRegexPatternsInSchema`,
`validateJSONPathSyntax`, `validateArrayLengthConsistencyChecks`) at all
four call sites that accept a `validation_schema`/`pre_validation` payload:
`update_validation_schema`'s executor, the generic step-update path
(`updateSingleStep`), step-creation validation, and evaluation-plan
normalization. Rejects any check pairing `pattern` with a `value_type`
other than `""`/`"string"`, naming the offending path and value_type.

~~Deliberately not touched: `update_evaluation_plan`'s own `pre_validation`
write path...~~ — fixed, see "Corrections applied" below.

## Verification

4 new tests in `value_type_pattern_compatibility_test.go`: rejects the
exact boolean+pattern shape from the findings, allows `string`+pattern,
allows a non-string `value_type` with no pattern, and a nil-schema no-op.
`go build ./...`, `go test ./pkg/orchestrator/agents/workflow/step_based_workflow/...`,
and `go test ./cmd/server/...` all pass.

## Reverify

No live schema-authoring call has exercised this guard against a real
contradictory schema yet. Reverify by confirming a future
`update_validation_schema`/step-creation call that pairs a non-string
`value_type` with a `pattern` is rejected with a clear error instead of
silently landing an unsatisfiable check.

## Independent review (2026-08-29)

**Root cause: checks out.** `validatePattern`
(`agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/pre_validation.go:835-849`)
really does reject any non-string value with the exact message quoted in
the ticket, `"Pattern validation only applies to strings"`
(`pre_validation.go:846`), regardless of what `value_type` claims. Since a
`value_type=boolean` (or any non-string type) check and a `pattern` check
are independent gates that must both pass, and no JSON value is
simultaneously a `bool` and a `string`, the combination genuinely can never
be satisfied — the ticket's core claim is correct.

**Fix: exists and is wired at write time as described.**
`validateValueTypePatternCompatibility`
(`planning_agent.go:2294-2320`) rejects exactly the described shape: it
walks `schema.Files[].JSONChecks`, and for any check with `Pattern != ""`
and a trimmed `ValueType` that is non-empty and not `"string"`, appends an
error naming the file, path, and offending `value_type`
(`planning_agent.go:2305-2311`). All four claimed call sites are real and
correctly identified:
- `planning_agent.go:3341`, inside `updateSingleStep` (func at `:2931`) —
  "the generic step-update path."
- `planning_agent.go:4709`, inside `createSingleStepAdder` (func at
  `:4666`) — "step-creation validation."
- `planning_agent.go:5950`, inside `createUpdateValidationSchemaExecutor`
  (func at `:5907`) — "`update_validation_schema`'s executor."
- `evaluation_helpers.go:178`, inside `registerEvaluationValidationTools`'s
  evaluation-plan normalization loop, gated on `step.PreValidation != nil`
  (`:168-181`) — "evaluation-plan normalization."

All 4 tests in `value_type_pattern_compatibility_test.go` pass as
described (boolean+pattern rejected and error names the path/value_type;
string+pattern allowed; non-string value_type with no pattern allowed;
nil-schema no-op). Independently ran `go build ./...`, `go test
./pkg/orchestrator/agents/workflow/step_based_workflow/... -run
ValueTypePattern -v`, and both pass. The "deliberately not touched"
claim about `evaluation_plan_tool.go`'s `update_evaluation_plan` path is
also accurate — grepping that file finds no call to any of the four
write-time validators; it references `pre_validation` only as a raw JSON
field name/schema-doc string (`evaluation_plan_tool.go:25`, `:208`).

**Wording overreach:** none found. The call-site count (4) is accurate,
unlike the sibling PLAT-235 ticket's off-by-one call-site claim.

**Missed edge case — DB validation checks share the exact same
unsatisfiable shape and are completely unguarded.**
`ValidationSchema` has two independent check surfaces:
`Files []FileValidationRule` (each with `JSONChecks
[]JSONValidationCheck`) and `DB []DBValidationRule` (each with `Checks
[]JSONValidationCheck`) — same struct definition, same `ValueType`/
`Pattern` fields (`planning_agent.go:153-160`, `:162-169`, `:204-214`).
`validateValueTypePatternCompatibility` only iterates `schema.Files`
(`planning_agent.go:2300`) and never touches `schema.DB`. This is not a
theoretical gap: at runtime, `evaluateDBRule`
(`pre_validation_db.go:45-74`) feeds every entry of `rule.Checks` through
`validateJSONCheck(ctx, c, first)` (`pre_validation_db.go:69`), the same
evaluator whose pattern step is the very `validatePattern` function this
ticket cites — so a DB check pairing e.g. `value_type=boolean` with a
`pattern` is exactly as unsatisfiable as the original file-check finding,
and the new write-time guard silently lets it through at every one of the
four call sites.

This blind spot is not unique to the new validator — all three sibling
write-time validators it sits alongside also only walk `schema.Files` and
never `schema.DB` (`validateRegexPatternsInSchema`,
`planning_agent.go:2257`; `validateJSONPathSyntax`, `:2331`;
`validateArrayLengthConsistencyChecks`, `:2422`), so this is a pre-existing
platform gap the new function inherited rather than a regression it
introduced. But the ticket's own quoted changelog reason text
("preserve boolean typing and every companion file/DB guard") shows DB
guards are an established, named concept in this schema, and the ticket's
"Deliberately not touched" section calls out only the
`evaluation_plan_tool.go` gap — it should also name this equally real,
currently-live DB-check blind spot rather than leaving it undisclosed.

**Corrections applied (2026-08-29):**

1. **`update_evaluation_plan` gap closed.** Added
   `validateSchemaLikeUpdateField` in `evaluation_plan_tool.go`: for either
   `validation_schema` or `pre_validation` in an update's raw JSON, it
   round-trips the value through `ValidationSchema` and runs all four
   write-time validators before the write proceeds. New test
   `TestUpdateEvaluationPlanRejectsUnsatisfiableValueTypePattern`
   reproduces the exact PLAT-236 shape through this tool and confirms it's
   now rejected.
2. **DB-check blind spot closed for all four validators, not just this
   one.** Added `forEachSchemaCheck`, a shared iterator walking both
   `schema.Files[].JSONChecks` and `schema.DB[].Checks`. All four
   validators (`validateRegexPatternsInSchema`, `validateJSONPathSyntax`,
   `validateArrayLengthConsistencyChecks`,
   `validateValueTypePatternCompatibility`) — and `ValidationSchema`'s own
   `UnmarshalJSON` double-escape-pattern fix — now route through it, so a
   DB check gets the identical write-time coverage a file check does. New
   test `TestSchemaValidatorsAlsoCoverDBChecks` (4 subtests, one per
   validator) proves each one now rejects the equivalent DB-check shape.

`go build ./...` and `go test
./pkg/orchestrator/agents/workflow/step_based_workflow/... ./cmd/server/...`
pass (13 total tests across both corrections).
