[← goals / steps / database](index.md)

# PLAT-238 — `query_workflow_db`/`mutate_workflow_db` had no `REGEXP` function registered, so any schema-declared REGEXP query failed uniformly

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | goals |
| Area | steps/database |
| Summary | harness_issue, severity high. |

| Coordination | Value |
|---|---|
| Assigned agent | Claude Code |
| Ticket state | `implemented; runtime reverify` |
| Last synchronized | `2026-08-29` |

- **Priority:** harness_issue, severity high.
- **Findings:** Twitter/social-media `PUL-9780ADDE` — a step declared a DB
  validation invariant using SQL `REGEXP`, and the managed query endpoint
  rejected it with `SQL logic error: no such function: REGEXP (1)` for
  every attempt, regardless of the actual data.

## Root cause, directly confirmed against the real code

SQLite's `REGEXP` operator has no built-in implementation. Per SQLite's own
documentation, `X REGEXP Y` is pure syntax sugar for calling an
application-registered `regexp(Y, X)` scalar function — without that
registration, SQLite itself returns exactly `no such function: REGEXP`, the
identical error text in the finding's evidence.

`workspace/handlers/query.go`'s `openQueryOnlyDB`/`openMutationDB` open the
DB via the `"sqlite"` driver (`modernc.org/sqlite`), which was imported only
as a blank import (`_ "modernc.org/sqlite"`) for side-effect driver
registration — nothing in this repo ever called
`sqlite.RegisterScalarFunction`/`RegisterDeterministicScalarFunction` to
register `regexp`. Any step whose validation schema declares a REGEXP-based
SQL check was therefore guaranteed to fail pre-validation on every attempt,
independent of whether the underlying data actually satisfied the
invariant — exactly the finding's own description ("Every run using the
declared REGEXP-based invariant fails pre-validation regardless of correct
receipts").

## Fix

`modernc.org/sqlite` does support registering custom scalar functions
(`RegisterDeterministicScalarFunction`). Added a package-level `init()` in
`workspace/handlers/query.go` registering a deterministic 2-arg `"regexp"`
function matching SQLite's own `regexp(pattern, value)` convention, backed
by Go's `regexp.MatchString`. Registration is process-global and applies to
every connection opened after it runs (both `openQueryOnlyDB` and
`openMutationDB`), so no other call site needed changes. NULL operands
return NULL (matching SQLite's own comparison-operator convention, not an
error); an invalid regex pattern surfaces as a query error rather than a
silent false match or a panic.

## Verification

3 new tests in `workspace/handlers/query_regexp_test.go`:
`TestQueryWorkflowDBSupportsRegexpOperator` (a real REGEXP query against
seeded rows returns exactly the matching ones, through the actual
`/api/query` HTTP handler), `TestQueryWorkflowDBRegexpRejectsInvalidPattern`
(a malformed pattern fails the query rather than silently passing), and
`TestQueryWorkflowDBRegexpTreatsNullOperandAsNull` (a NULL value never
matches, doesn't error). `go build ./...` and `go test ./...` pass for the
entire `workspace` module.

## Reverify

This fix lives in the `workspace` module (the workspace-api service), which
is a separately deployed process from `agent_go` — the running server was
not restarted this session (out of scope without separate authorization).
Reverify by confirming a future REGEXP-based DB validation check on a live
deployed instance succeeds instead of failing with `no such function:
REGEXP`.
