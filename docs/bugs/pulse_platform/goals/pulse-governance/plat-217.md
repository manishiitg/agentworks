[← goals / pulse-governance](index.md)

# PLAT-217 — the all-or-nothing review-persist failure mode described in the finding no longer exists; the current architecture is structurally per-finding

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P3 |
| Product | goals |
| Area | pulse-governance |
| Summary | P3 — the reported incident (2026-08-03) was real for the |

> **2026-09-10 update:** [PLAT-306](plat-306.md) supersedes mandatory Markdown
> checkpoint/report maintenance with optional SQLite review notes on the existing
> result tool. Typed lifecycle records remain authoritative; historical design
> discussion below is retained. No reporting-only turn is required.

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `resolved` — architecture changed since the finding was filed; no code change needed |
| Last synchronized | `2026-08-29` |

- **Priority:** P3 — the reported incident (2026-08-03) was real for the
  architecture in place at the time, but that architecture — one atomic
  Markdown blob with embedded structured markers, validated and persisted
  all at once — no longer exists.
- **Owner:** N/A — no code change made or needed by this ticket.
- **Related:** `harness:pulse-review-persist:malformed-marker-discards-whole-review`
  (`Workflow/HDFC-Personal-Accounts`, high) — the finding this closes.
  [PLAT-055](../../platform/learnings-knowledge/plat-055.md)) (the earlier ticket, already landed before this
  session, that began the migration to typed per-finding tool calls).

## The finding

On 2026-08-03, `eval_health`'s review artifact was entirely missing from
`pulse_review_log` for one review run — not recorded even with a
`contract_failed` status — while a sibling `artifact_review` row for the
same run persisted normally. The finding's evidence cites guidance text
describing the intended behavior (*"the backend retained its raw Markdown
but quarantined its invalid structured verification markers"*) as the
promised-but-unmet contract.

## What was searched for, and found absent

Searched the current codebase for any code implementing "validate a whole
Markdown blob's embedded structured markers, quarantine the invalid ones,
keep the raw Markdown" — the mechanism the finding describes as broken:

- `PULSE_VERIFICATION_JSON` (the marker format the evidence names): zero
  matches in any non-test Go file.
- `quarantine`: zero matches in the reviewer/finding-lifecycle code.
- The only `contract_failed` reference in the current code is a status
  value excluded from a query filter — nothing writes that status or
  performs the all-or-nothing validate-then-discard behavior described.

Traced the two functions that actually persist review outcomes today:

- `CompletePulseReview`/`recordPulseReviewOnDB` — writes one module's
  terminal `pulse_review_log` row (verdict, status, verification list) per
  call. No multi-marker blob parsing.
- `RecordPulseReviewFinding`/`validateTypedPulseReviewFinding` — validates
  and records **one finding at a time**, via a typed, structured tool call
  (`record_pulse_finding`) with explicit fields (concern, classification,
  severity, evidence, ...), not an embedded marker scraped from free text.
  A validation failure on one call returns an error for that one call; it
  cannot touch any other finding's already-persisted row, because there is
  no shared transaction or blob spanning multiple findings to begin with.

This mirrors the PLAT-055 migration already documented in
`run_concern_tool.go`'s own header comment (and confirmed independently
this session while investigating PLAT-211): the platform moved from
free-text `CONCERNS:` markers parsed out of a single completion summary to
typed, structured, one-call-per-finding tools specifically because the old
shape "guarantees capture of lines, not concerns." The same shift removes
this finding's failure mode by construction — there is no longer an atomic
"whole review persist" for a bad marker to take down.

## Explicitly not done

- Did not attempt to reproduce the original 2026-08-03 incident — the code
  path it describes is confirmed absent, not merely hard to trigger.
- Did not audit whether `pulse_review_log`'s legacy `artifact_markdown`
  column (still read for backward compatibility, per
  `pulse_review_log.go`'s migration-copy logic) could still exhibit a
  similar loss for very old, pre-migration records — out of scope; this
  ticket is about the current write path, not historical data.

## Verification

- Confirmed via `grep` across the full `agent_go` module (non-test files)
  that no code references `PULSE_VERIFICATION_JSON` or performs
  quarantine-style partial marker validation.
- Read `CompletePulseReview`, `recordPulseReviewOnDB`, and
  `RecordPulseReviewFinding`/`validateTypedPulseReviewFinding` directly to
  confirm the current write path is per-finding, not per-blob.
- No code changed by this ticket — nothing to build or test.
