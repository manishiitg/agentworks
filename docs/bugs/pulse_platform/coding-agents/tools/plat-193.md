[← coding-agents / tools](index.md)

# PLAT-193 — `toolerr` misclassified `execute_shell_command` success as failure when stdout content happened to contain JSON with a `success`/`status` field

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | coding-agents |
| Area | tools |
| Summary | P2 — a platform-wide false-positive-failure risk on the most |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `fixed` |
| Last synchronized | `2026-08-28` |

- **Priority:** P2 — a platform-wide false-positive-failure risk on the most
  commonly used tool (`execute_shell_command`), reproduced directly during a
  confida-login Pulse verification pass, not just theorized.
- **Owner:** `mcpagent/toolerr/toolerr.go` — shared by the in-process agent
  loop and the HTTP bridge handlers (one classifier, per the package's own
  doc comment, specifically to avoid two copies drifting).
- **Related:** `harness:execute_shell_command:opaque-error-envelope`
  (medium), the confida-login harness finding this addresses.

## The reported incident

`cat` on a file containing a captured API error response (raw JSON body
starting with `{"success":false,"error":"..."}`) failed with the opaque
`"tool execution failed: tool execution failure envelope"` message and no
stderr/exit code, even though `cat`'s own exit code is unconditionally 0 for
reading an existing file. A second call reading the same file via
`python3 -c "open(...).read()"` (which doesn't echo the raw JSON the same
way) succeeded and revealed the true content — confirming the file itself
was fine and the classification, not the shell command, was wrong.

## Root cause — confirmed in code, not guessed

`toolerr.canonicalFailureValue` is the shared "does this tool result look
like a failure" classifier (`[TOOL_ERROR_SUSPECT]`), deliberately
"field/envelope-aware" per its own doc comment — it recurses into known
transport-envelope fields (`content`, `text`, `result`, `stdout`, `stderr`,
`error`) and JSON-decodes their string content to check for structured
failure signals (`success: false`, `is_error: true`, `status: "failed"`,
etc.), specifically so a bridge result JSON-stringified inside an MCP
`content`/`text` block still gets classified correctly.

The bug: `stdout` and `stderr` were included in that same recursive
JSON-decode-and-inspect list. For most tools this is harmless, but
`execute_shell_command`'s `stdout` is a shell command's **arbitrary output**,
not a nested transport envelope — and the function's own comment states
exactly the principle this violated: *"Recurse through known
transport-envelope fields, not arbitrary domain data. A tool may
legitimately return a record whose own status is 'failed'; inspecting every
nested value would turn that discussion into a tool failure."* `cat`-ing a
file whose bytes happen to be `{"success":false,...}` — legitimate captured
API-response data — tripped exactly that.

## Fix attempt #1 (reverted) — too broad

First attempt removed `stdout`/`stderr` from the recursion list entirely.
This broke two existing tests: stdout's own PLAIN TEXT literally starting
with `"ERROR: ..."` or matching `"tool execution failed: ..."` is a genuine,
intended self-reported-failure signal (a CLI tool that prints an error and
exits 0 anyway) — not the same thing as JSON-decoding stdout and inspecting
*nested* structure inside it. Removing stdout/stderr from the recursion
entirely lost that legitimate signal too. Caught by the existing test suite
before shipping, not by manual review.

## Fix — precise: gate the JSON-decode step, not the field itself

`stdout`/`stderr` still participate in the recursion and are still checked
against the existing plain-text prefix patterns (`"error:"`,
`"tool execution failed:"`, an HTTP failure status line, and — for
`stderr` specifically — permission-denial wording). What changed: a new
`jsonEnvelopeFields` allowlist (`""`, `content`, `text`, `result`, `error`)
gates the `decodeJSONValue`-and-recurse step specifically — only fields
known to actually carry a nested transport envelope get their string content
parsed as JSON and walked for `success`/`status`/`is_error` fields. `stdout`
and `stderr` are excluded from that allowlist, so their content is checked
as *text* (prefix matching) but never re-interpreted as *structure*.

## Explicitly not done

- No change to `problemReportingTools` (the existing full-suppression
  allowlist) — `execute_shell_command` genuinely can have real transport
  failures worth catching (non-zero `exit_code`, stderr permission-denial
  text), so full suppression for this tool was never the right fix, only the
  narrower stdout/stderr-as-structure path.
- Did not audit every other tool whose result might route through `stdout`/
  `stderr`-shaped fields for a similar but differently-named pattern; scoped
  to the exact reported and reproduced mechanism.

## Verification

- `go build ./...` clean across the whole `mcpagent` module.
- Full `toolerr` package suite (22 tests) passes, including the two tests
  the first (reverted) fix attempt broke —
  `TestCanonicalFailureUnwrapsHighConfidenceNestedFailures` and
  `TestSuspiciousForToolCatchesHarnessEnvelopeWrappedInShellSuccess` — both
  now pass against the precise fix.
- New test `TestCanonicalFailureDoesNotDecodeStdoutAsANestedEnvelope`
  reproduces the exact reported shape (a captured API error response inside
  `stdout`, and the same shape inside `stderr`) and proves both are now
  classified as success, matching the real `exit_code: 0`.
- Not yet live-verified: no real workflow has hit this corrected path in
  production since it shipped.
