[← goals / evaluation](index.md)

# PLAT-229 — a JSON array value could pass a `value_type=string` pre-validation check

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | goals |
| Area | evaluation |
| Summary | P2 in the audit queue, but `severity: high` on the finding |

| Coordination | Value |
|---|---|
| Assigned agent | Claude Code |
| Ticket state | `implemented; adjacent wildcard predicates fixed; wildcard value_type=array and empty-parent contracts fixed; runtime reverify` |
| Last synchronized | `2026-09-09` |

- **Priority:** P2 in the audit queue, but `severity: high` on the finding
  itself — a shared correctness bug in message-sequence/step pre-validation,
  not a cosmetic gap.
- **Owner:** `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/pre_validation.go`,
  `validateJSONCheck`/`validateValueType` — the shared JSON schema validator
  behind every step's `validation_schema`/`pre_validation` checks
  platform-wide, not workflow-scoped code.
- **Finding:** LinkedIn `PUL-61C84987`.

## The reported symptom

A retained `engagement_targets.json` artifact had `$.notes` typed `array` in
practice. Its automatic pre-validation receipt recorded
`$.notes value_type Passed=true` under `value_type=string` and an overall
`overall_pass=true` — a wrong-shaped artifact passed as if it matched its
declared schema.

## Root cause, confirmed in code

`validateJSONCheck` extracts the value at `check.Path` before type-checking
it against `check.ValueType`. `jsonpath.Get` (PaesslerAG/jsonpath) returns
Go's `[]interface{}` for two genuinely different situations that are
indistinguishable by type alone:

1. The path names exactly one location, and that location's own JSON value
   happens to be an array (e.g. `$.notes` where `notes: ["some note"]`).
2. The path contains wildcard/filter/slice syntax and matched multiple
   separate locations (e.g. `$.checks[*].name`).

The existing code resolved that ambiguity using `check.ValueType` as a
heuristic: if the expected type was `"array"`, treat the `[]interface{}` as
the value itself; otherwise, treat it as a multi-match collection and take
its first element for a scalar check. That heuristic is exactly backwards
in the failure case that matters: when the *actual* data is an array but the
*schema* expects `"string"` — precisely a type mismatch the check exists to
catch — the code took the array's first element instead. For a
string-containing array like `["some note"]`, that first element is itself
a string, so `validateValueType` saw a string and passed. The wrong
container type slipped through undetected.

## Fix

Added `jsonPathHasMultipleMatches(path string) bool` — a small pattern
match for genuine JSONPath multi-match syntax (`*`, `..`, `?(`, slice/union
index groups like `[0:2]`/`[0,1]`). A plain numeric index (`[0]`, per the
struct's own doc-comment example `"$.databases[0].name"`) or a bare field
path does not match; the struct's own example of a definite path only
proves one location. The extraction now only applies the multi-match /
"take first element" handling when the path genuinely can match more than
one location. For a definite path, whatever `jsonpath.Get` returns *is* that
one location's value, whatever its type — never unwrapped.

## Verification

```text
go test ./pkg/orchestrator/agents/workflow/step_based_workflow/...
go test ./pkg/orchestrator/... ./cmd/server/...
```

New coverage:

- `TestValueTypeCheckRejectsAnActualArrayValueOnADefinitePath` — reproduces
  the exact reported shape (a non-empty string-array, an empty array, and a
  multi-element array), all now correctly rejected against
  `value_type=string`.
- `TestValueTypeCheckStillPassesARealString` — the finding's own positive
  control.
- `TestValueTypeCheckStillAcceptsAnArrayValueWhenArrayIsExpected` — proves
  the pre-existing, correct `value_type=array` case is untouched.
- `TestValueTypeCheckStillUnwrapsGenuineWildcardMatches` — proves the fix is
  scoped to definite paths only; a real wildcard multi-match still takes its
  first result exactly as before.
- `TestJSONPathHasMultipleMatchesDistinguishesDefiniteFromWildcardPaths` —
  pins the exact boundary, including that `[0]` (a definite index) is not
  mistaken for multi-match syntax merely because it contains brackets.

Full existing package and broader orchestrator/`cmd/server` suites pass
unchanged.

## Reverify

No live pre-validation run has exercised the corrected path through the
deployed server yet. Reverify per the finding's own `next_check`: run
scratch `engagement_targets.json` fixtures under the retained schema and
confirm `notes=[]` now fails `$.notes value_type=string` with
`overall_pass=false`, while `notes:"..."` (string) still passes.

## Code review follow-up (2026-08-29)

A second, related gap survived this fix: for a genuine wildcard multi-match
path (e.g. `$.items[*].name`), `value_type` only ever inspected the first
matched result — a valid first item followed by a wrong-typed later one
still passed `overall_pass=true`, unnoticed. This was pre-existing behavior
this ticket deliberately preserved
(`TestValueTypeCheckStillUnwrapsGenuineWildcardMatches`), not something this
fix introduced, but it undermines the same trustworthiness this ticket was
about.

Fixed narrowly: when a genuine multi-match path has more than one result and
a non-array expected type, every matched value is now checked, and the
first mismatch is reported by index (`"match 2 of 3: ..."`). At the time,
the other checks on the same path (`min_length`, `min_value`/`max_value`,
`pattern`) were left validating only the first matched value — see the
independent review below, which correctly flagged this as the same gap in
adjacent code, since fixed.

New test: `TestValueTypeCheckValidatesEveryWildcardMatchNotJustTheFirst`,
reproducing exactly this shape. Full existing suite, including the
single-match-still-unwraps test above, continues to pass.

## Independent review (2026-08-29)

The reported definite-path array-versus-string bug is fixed, and the
follow-up correctly checks every wildcard result for `value_type`. One
adjacent correctness gap remains in the same function: `min_length`,
`max_length`, numeric range, and regex `pattern` checks still operate only on
the first wildcard match. For example, a `$.items[*].status` pattern can pass
when the first item is valid and a later item is invalid.

The recommended deterministic rule is that every value returned by a
wildcard path must satisfy every configured per-value predicate, with the
failing match index included in the error. This does not reopen the original
`PUL-61C84987` disposition, but it should remain a visible validator follow-up
rather than being lost behind the narrower type fix.

**Correction applied (2026-08-29):** `min_length`/`max_length`,
`min_value`/`max_value`, and `pattern` now all route through the same
`checkEveryMatch` predicate the `value_type` fix introduced, so every check
on a genuine multi-match path validates every matched value and reports the
failing index the same way. New test
`TestAdjacentPerValuePredicatesValidateEveryWildcardMatch` (4 subtests: each
predicate individually, plus an all-valid-passes case). Full suite passes.

**Second correction applied (2026-08-29):** a further review found
`value_type="array"` on a genuine wildcard multi-match path was special-cased
to check whether the *whole multi-match result slice* was itself a Go
`[]interface{}` — trivially always true, since the matched-results collection
is always a slice regardless of what any individual matched value actually
was. E.g. `$.items[*].tags` with `value_type=array` would report passed even
if one item's `tags` field was a plain string, not an array. Removed the
special case entirely: `value_type=array` on a multi-match path now routes
through the same `checkEveryMatch` predicate as every other check, requiring
every individual matched value to itself be an array. A definite (non-wildcard)
path's own array value is unaffected — still checked directly, not per-element.
3 new tests: rejects a wildcard match where one item's value isn't an array
(reports the failing index), passes when every match is an array, and a
definite-path control case confirming single-value array checks are
untouched. Full suite passes.


## 2026-09-09 local Pulse audit follow-up

Substack `PUL-F219BEE9` reproduced an adjacent shared validator defect:
`engagements=[]` is valid under the parent array's `min_length=0`, but seven
required `$.engagements[*].field` checks failed and caused three repair attempts.
Removing per-item checks from successive workflow steps is not the platform fix.

Implemented: expand an explicit array wildcard before JSONPath flattens matches.
Every existing item must satisfy the nested contract, including `must_exist`;
a present empty array has no items to violate it. An explicit parent
`min_length=1` still rejects emptiness. Missing/wrong parent values and missing
fields in populated arrays still fail required checks. Definite-path values,
per-item array typing and all-match predicates retain their checks.

Regression coverage: empty no-action outputs, parent cardinality, missing
parents/fields, and a valid first item followed by a malformed item. Local tests
pass; deployed workflow acceptance remains pending. Historical workflow schemas
that removed checks are not silently rewritten by this platform change.
