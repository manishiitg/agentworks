[← platform / learnings-knowledge](index.md)

# PLAT-128 — guidance tests assert content for two docs that were deliberately deleted 8 days before

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P3 |
| Product | platform |
| Area | learnings-knowledge |
| Summary | P3 — nothing product-facing was wrong; a test suite was |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `implemented` (5 of 24 baseline failures); remainder is unrelated wording drift, deferred |
| Last synchronized | `2026-08-17` |

- **Priority:** P3 — nothing product-facing was wrong; a test suite was
  asserting behavior for a doc the product no longer has.
- **Owner:** `cmd/server/guidance/render_all_test.go`,
  `pkg/orchestrator/agents/workflow/step_based_workflow` prompt tests

## How it surfaced

Auditing tool-error logs for PLAT-125–127 kept surfacing a permanently-red
24-failure baseline in `cmd/server`, `guidance`, `virtual-tools`, and
`step_based_workflow`. A red baseline is exactly what let real regressions
hide earlier in this diagnosis — worth clearing rather than living with.

## Two unrelated root causes, not one

**A test/production drift, unrelated to any deleted doc.**
`TestReviewPlanPromptPrefersCoherentAgenticSteps` and
`TestReadOnlyExecutionPromptCannotRecommendMutationOrRawSQLite` build their
own hand-written `templateVars` map and call `ExecuteTemplate`/the prompt
processor directly. The first was missing `"DBGuidance"`, a key every real
caller (`WorkflowPlanReviewAgent.Execute`) sets before rendering the same
template — a plain test bug. The second asserted the read-only execution
prompt must never contain the substring `mutate_workflow_db` anywhere, which
became impossible once the guidance was made more explicit: `"This session is
read-only: do not call `mutate_workflow_db`."` (`BuildManagedWorkflowDBGuidance`,
`prompt_sections.go:79`) necessarily names the tool it forbids. Naming the
forbidden tool is correct, explicit guidance — the same shape as this
session's `record_run_concern` refusal fix (PLAT-123's identity-gap redirect)
and the `tools_unavailable` message itself. The test's own name says the real
intent: *cannot **recommend** mutation*, not *cannot mention it*.

**A deliberate architecture change the tests never caught up with.**
`git log --diff-filter=D -- '*review-improve-log*' '*review-code*'` finds
`0174b6aff "simplify Pulse workflow reviews"`, 2026-08-08 — 9 days before this
ticket. That commit deleted `templates/review/review-code.md`,
`templates/review/bug-review.md`, and (per an earlier commit in the same
lineage) the `review-improve-log` / `review-improve-log-migration` /
`review-improve-log-skeleton` trio, and rewrote `goal-advisor.md` from 538
lines down to a fraction of that. `render_all_test.go` still called
`renderFromRegistry("review-improve-log", ...)` and `renderFromRegistry("review-code", ...)`
directly — kinds absent from both `guidance.go`'s registry and the filesystem
— across 8 test functions and roughly 90 assertion lines describing
`builder/improve.html`'s exact HTML contract: `no_terminal_packet`,
`retry_due`, `approved_awaiting_evidence`, a skeleton markup spec, an archive
migration contract. `improve.html` itself is confirmed fully retired — Pulse's
own DB-backed findings (`record_pulse_finding`, `record_pulse_result`) and the
SQLite Pulse popup replaced it.

`review-code` (main.py-vs-step-description drift checking) has no live
caller anywhere in the codebase today, and its own test assertion expected
`data-module="bug_review"` — the *other* deleted kind's identity, not its
own — evidence the two were never cleanly separated even before removal.
Owner decision: leave it removed from the test suite; whether to design a
replacement is a separate, deliberate task, not something to restore
unexamined from an 8-day-stale file.

## Fix

- `execution_only_agent_test.go`, `maintenance_reviewer_prompts_test.go`: add
  the missing `DBGuidance` key; change the read-only-prompt check from
  substring-absence to asserting the explicit prohibition is present and no
  *recommending* phrase (`"Use `mutate_workflow_db`"`) is.
- `render_all_test.go`: removed every `review-improve-log`/`-migration`/
  `-skeleton`/`review-code` assertion — whole functions where that was their
  entire content (`TestReviewImproveLogMigrationIsExtracted`), single map
  entries or list items elsewhere. `TestPulseSpecialistsReturnStructuredPacketsAndParentOwnsHTML`
  additionally dropped a `"builder/improve.html"` content requirement,
  confirmed absent from all 10 remaining live specialist templates by
  rendering each one — not a guess, and not specific to `review-code`.

## Verification

Fail-before/pass-after (stash the 3 changed files, rerun, restore). Full
4-package regression run on current `origin/main`: 24 pre-existing failures
before, 19 after, zero new — the 5 fixed here are exactly the ones this
ticket addresses.

## Not fixed here

- **19 failures remain**, all single-phrase wording drift in templates that
  are still live and correctly registered (`pulse-setup`, `improve-evaluation`,
  `goal-advisor`, scheduler preflight-count tracking, and others) — a
  different, larger body of work needing individual judgment per assertion
  about whether the test or the content drifted. Not attempted here.
- **Whether `review-code` (or a successor) should exist** is an open product
  question, deliberately not decided by this ticket.
