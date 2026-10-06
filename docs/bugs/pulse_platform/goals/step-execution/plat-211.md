[← goals / step-execution](index.md)

## Internal reconciliation — 2026-09-05

PUL-D50AD8BC: historical ordinary-step registration requirement closed as superseded. Current tool-profile exclusion tests pass; normal CONCERNS scraping was not restored.

Resolved in SQLite for internal tracking with previous concern/detail records
preserved in resolution events. Source/tests verified; deployed replay and
historical business/module-result repair are not claimed. Full mapping:
[remaining-report audit](../../../../audits/platform-open-report-reconciliation-2026-09-05.md).

# PLAT-211 — retain Markdown-wrapped `CONCERNS:` parsing only for legacy/advisor compatibility; normal workflow-step ingestion is retired

| Field | Value |
|---|---|
| State | closed |
| Priority | P3 |
| Product | goals |
| Area | step-execution |
| Summary | P3 — this was a real historical loss in parser-backed |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `retained legacy compatibility` |
| Last synchronized | `2026-08-29` |

- **Priority:** P3 — this was a real historical loss in parser-backed
  ingestion. Normal step `CONCERNS:` scraping is now deliberately retired:
  steps must use explicit, structured Pulse finding routes. The parser remains
  only for legacy records and strategic-advisor route validation.
- **Owner:** `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/run_concerns.go`
  (`ParseConcernLines`).
- **Related:** `harness:pulse-concerns-parser:backtick-wrapped-marker-silently-ignored`
  (`Workflow/HDFC-Personal-Accounts`, high) — the finding this fixes. First
  cross-workflow ticket filed this session outside `confida-login`, after
  the user asked to also check other workflows' platform harness findings.

## The finding

A reviewer's completed review artifact (`pulse_review_log`,
`module=artifact_review`) contained six `CONCERNS:` lines, each rendered as
a Markdown inline-code span — `` `CONCERNS: ...` `` — rather than a bare
line. All six produced zero `run_concerns` rows: no error, no warning, the
review still recorded `status=completed`. In the same database, two *bare*
`CONCERNS:` lines from a different module (`goal_advisor`) filed correctly
the same day, confirming the parser itself (not the review pipeline) was
the point of loss.

## Root cause — confirmed in code

`ParseConcernLines` matched the marker via
`strings.HasPrefix(strings.ToUpper(trimmed), "CONCERNS:")` on each
whitespace-trimmed line. A line opening with a backtick — `` `CONCERNS: ... ` ``
— starts with `` ` ``, not `C`, so the prefix check never matched; the line
was silently skipped like any other non-marker line, with no distinction
between "not a concern" and "a concern the parser failed to recognize."

## Retained compatibility fix

Added `stripMarkdownCodeSpan`, called before the prefix check: removes a
single matching pair of backtick-run delimiters that wrap the **entire**
line (Markdown inline code spans open and close with the same run length of
one or more backticks). Deliberately conservative — it only strips when the
same-length run opens and closes the whole trimmed line, and refuses if the
closing delimiter also appears inside the span (a sign of multiple spans on
one line, not a single whole-line wrap), leaving those lines untouched
rather than guessing. A `CONCERNS:` line with an *internal* backtick
(`` CONCERNS: the `foo` field is stale ``) is unaffected either way, since
it never starts with a backtick in the first place.

This is intentionally **not** a normal workflow repair path anymore. The
2026-08-29 Pulse simplification removed Go-side harvesting of ordinary step
completion summaries and removed `record_run_concern` from normal step tools.
Keeping this small parser behavior protects historical/legacy artifacts and
the strategic-advisor validation helper without reviving broad text scraping.

## Explicitly not done

- Did not attempt to handle every conceivable Markdown rendering of the
  marker (e.g. bold `**CONCERNS:**`, a line inside a fenced code block) —
  scoped precisely to the one confirmed, evidence-backed shape (a whole-line
  inline-code span), matching what was actually observed rather than
  speculatively hardening against unobserved variants.
- Did not add a warning/error path for a line that starts with a marker-like
  token but fails to parse as one — out of scope for this fix, which closes
  a specific silent-loss mechanism rather than redesigning the parser's
  failure signaling.

## Verification

- `go build ./...` clean.
- New `TestParseConcernLinesUnwrapsMarkdownCodeSpan` covers: a whole-line
  wrap (the reported shape) extracts correctly; whitespace inside the span
  is tolerated; an internal (non-wrapping) backtick is left alone; an
  unterminated span is correctly not treated as a marker; two adjacent
  spans on one line are correctly left unstripped rather than guessed at.
- Existing `TestParseConcernLinesExtractsPayloadOnly` passes unchanged.
- Full `pkg/orchestrator/agents/workflow/step_based_workflow` suite passes.
- Full suite: 3 pre-existing failures before and after (`cmd/server/guidance`,
  unrelated content), no regression.
