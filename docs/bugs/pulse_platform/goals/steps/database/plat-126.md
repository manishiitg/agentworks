[← goals / steps / database](index.md)

# PLAT-126 — an unquoted JSON path to `json_extract` fails with an error that names the symptom, not the fix

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | goals |
| Area | steps/database |
| Summary | P2 — no data is lost or corrupted; a read or a write simply |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `implemented` — SQLite hint and shell-safe HTTP-bridge payload contract shipped; current binary verified the hint, restart/runtime reverify remains for the prevention guidance |
| Last synchronized | `2026-08-19` |

- **Priority:** P2 — no data is lost or corrupted; a read or a write simply
  fails, repeatedly, without the caller ever learning why.
- **Owner:** `cmd/server/virtual-tools/workflow_db_tools.go`
  (`query_workflow_db` / `mutate_workflow_db`)

## How it surfaced

Auditing tool-error logs for PLAT-125 turned up 22 identical-shaped failures
in one day, all from `social-media`, concentrated in one message-sequence
step (`step-exec-quote-tweet`, 12 of them, four in the same second):

```
error="HTTP 400: {\"success\":false,\"message\":\"Query failed\",
       \"error\":\"SQL logic error: unrecognized token: \\\"$\\\" (1)\"}"
sql=SELECT json_extract(v.value, $.signal_id) AS signal_id,
           json_extract(v.value, $.type) AS type, ...
```

The path argument to `json_extract` must be a quoted string —
`json_extract(col, '$.field')` — and the agent wrote it bare:
`json_extract(col, $.field)`. SQLite reserves `$` (and `@`, `:`) to introduce
a bind parameter directly in SQL text (`$name`, `@name`), so it tries to lex
a parameter name, the following `.` breaks that, and it reports the sigil
itself — `"$"` — as the unrecognized token. Three further occurrences used
`@` the same way: `ltrim(handle, @)`, meant to strip a leading `@` from a
handle, written without quoting the literal.

## Why this is worse than an ordinary syntax error

A caller who gets `no such column: input_id` can act on it — the platform
already has a fix for exactly that shape (`workflowDBSchemaHintError`, added
for the identical reason: *"one overnight run spent 18 tool calls inventing
column names."*). `unrecognized token: "$"` gives no such foothold. It names
a character, not a mistake. Nothing in the message says "this looks like an
unquoted JSON path" or "quote it." The caller is left to reverse-engineer a
SQLite parser error, and 22 identical failures in one day says that mostly
didn't happen — the same wrong query was retried unchanged rather than
corrected.

`stores.md`, the reference doc that introduces `json_extract` to step agents,
made this worse by omission: it named the function
(*"`json_extract` to read them back"*) without ever showing its syntax, so
there was nothing for the agent to check its own call against.

## Fix shipped

**Detection** — `workflowDBUnrecognizedSigilPattern`
(`workflow_db_tools.go`), a regex matching SQLite's own rendering of this
specific failure across however many layers of JSON re-escaping the error
text has passed through by the time it reaches the tool caller (`"$"`,
`\"$\"`, or deeper).

**Explanation** — `workflowDBUnquotedBindSigilHint` wraps the original error
(never discards it) with what SQLite does not say: that `$`/`@` introduce a
bind parameter, that the fix is quoting (`json_extract(col, '$.field')`,
`'@'`), spelling out every `json_*` function the path argument applies to.
Wired into **both** `query_workflow_db` and `mutate_workflow_db` — the mutate
path previously enriched no errors at all, not even the existing
schema-hint.

**Guidance** — `stores.md` now shows the corrected form and states plainly
that SQLite reads an unquoted `$`/`@` as a bind parameter, not a JSON path,
so the syntax is checkable before the call is made, not only after it fails.

## Verification

Six tests in `workflow_db_sigil_hint_test.go`, all against the real
production path — `startWorkflowDBSchemaHintServer`'s real SQLite file
through the real `POST /api/query` handler (query), and a matching
`startWorkflowDBMutationServer` through the real `POST /api/mutate` handler
with a session actually granted `db_access=read-write` (mutate) — not a
hand-written stand-in for SQLite's error text:

- unquoted `json_extract(col, $.field)` on both `query_workflow_db` and
  `mutate_workflow_db` gets the hint, with the original SQLite error still
  present in the message
- unquoted `@` in a string function gets the hint
- a correctly quoted `json_extract(col, '$.field')` is completely unaffected
  — same result as before this existed
- an unrelated syntax error (`SELECT FROM ...`) does not fire the hint

Confirmed fail-before/pass-after by reverting the fix (stashing
`workflow_db_tools.go` alone) and re-running: the three positive tests fail
on the unmodified code, pass with it restored.

Full-package regression run (`cmd/server`, `guidance`, `virtual-tools`,
`step_based_workflow`) against `origin/main` at PLAT-125 (`b7914e80d`): 24
pre-existing failures before this change, 24 after — zero new, zero fixed.

## Not fixed here

- **The mutate path had no error enrichment of any kind before this** — not
  the sigil hint, not the existing schema hint. Only the sigil hint was added
  to it; `no such column`/`no such table` on a mutation still returns
  SQLite's bare text. That gap predates this ticket and is not new.
- **This does not auto-correct the SQL.** Consistent with how the rest of
  this register has handled recoverable mistakes this session (the
  `record_run_concern` identity refusal, `tools_unavailable`'s own message) —
  name the destination or the fix precisely, never guess on the caller's
  behalf.

## 2026-08-19 live follow-up — the shell corrupted correct-looking SQL before HTTP

The manual Social Media schedule
`schedule-manual--5227790a_1787115853755603000`, Intent Queue child
`msgseq-iteration-0-default-default-step-6-sub-execute-intent-queue-...`,
reproduced the failure after the original fix was live. This time the command
text visibly contained the correct SQL form:

```bash
curl --json '{"sql":"SELECT json_extract(v.value,'$.signal_id') ..."}' ...
```

There is one runtime path: agent -> `execute_shell_command` -> curl -> HTTP
bridge -> `query_workflow_db`. The request JSON and inner SQL string both used
shell single quotes. Single quotes do not nest in POSIX shells, so the shell
removed the quotes around `$.signal_id` before curl built the HTTP request. The
DB tool received the same invalid bare path as before. The production handler
returned PLAT-126's enhanced explanation in 1.3 ms, proving the diagnostic fix
was deployed, but the agent repeated the command because its source still
appeared quoted.

The failed outer `execute_shell_command` also lacked a matching UI `END`
receipt. That separate receipt defect remains PLAT-141 and is not owned here.

### Follow-up fix

The single HTTP-bridge contract now defines one safe encoding rule: quoted,
multiline, SQL, JSON-path, and other nontrivial values stay in shell variables
and `jq -n --arg` performs JSON escaping. The rule is exposed consistently in:

- mcpagent's universal code-execution prompt;
- every workflow step's managed-DB prompt, without requiring an optional
  reference read;
- the `sql` parameter descriptions returned by `get_api_spec` for both DB
  tools;
- the MCP bridge and persistent-store reference docs.

Canonical call:

```bash
sql="SELECT json_extract(v.value, '$.signal_id') FROM critic_signals"
payload="$(jq -cn --arg sql "$sql" '{sql:$sql}')"
curl --fail-with-body -sS --json "$payload" -H "$MCP_AUTH" "$MCP_CUSTOM/query_workflow_db"
```

The backend does not rewrite SQL or guess lost quotes. It cannot safely recover
characters removed before HTTP. The prevention fix preserves the exact SQL
bytes the agent authored before the request reaches existing validation and
permission checks.
