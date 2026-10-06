[← goals / step-execution](index.md)

## Internal tracking resolution — 2026-09-05

Related follow-up resolved: RTS PUL-4FE07CD2. Nonzero saved-script exits can no longer be promoted to success by passing artifact validation. Exit-2 refusal and repair fallback remain unchanged. Focused scripted tests pass; this was separate from the original PLAT-216 fix.

Closed at the user's request for internal tracking. Fixes are tested local,
uncommitted source changes based on `0babf193ec0efdf33511a3150f82e0b29685814e`;
deployment verification is pending. No live workflow or historical schedule
receipt was modified. Prior investigation below is retained as history.

# PLAT-216 — a scripted step's deliberate fail-closed refusal was treated identically to a crash, so the agentic fallback overrode the refusal it agreed was correct

| Field | Value |
|---|---|
| State | closed |
| Priority | P1 |
| Product | goals |
| Area | step-execution |
| Summary | P1 — real financial-data-integrity incident. |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `fixed` |
| Last synchronized | `2026-08-29` |

- **Priority:** P1 — real financial-data-integrity incident. A fail-closed
  guard written specifically to prevent overwriting bank balance history
  with unverifiable data was read, agreed with, and then overridden by the
  runtime's own fallback mechanism on the same run.
- **Owner:** `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_scripted.go`
  (`decideScriptedFastPath`, `tryRunSavedScriptedScript`),
  `controller_execution.go` (the scripted-step relearn dispatch),
  `cmd/server/guidance/templates/system/code-authoring.md`.
- **Related:** `harness:scripted-step-fallback:protective-abort-overridden-by-agentic-retry`
  (`Workflow/HDFC-Personal-Accounts`, high) — the finding this fixes.

## The finding

`update-bank-balance`'s `main.py` implements a BR5-01 fail-closed guard: if
it cannot verify the existing "Bank Balance" rows before writing, it
`sys.exit(1)`s with an explicit refusal (`'ABORT: failed to read existing
"Bank Balance" rows ...; refusing to overwrite and risk wiping history'`)
rather than risk corrupting real balance data. On 2026-08-05, the guard
fired correctly — and the runtime's own agentic fallback then read that
exact refusal, its own completion summary stating *"the script correctly
fail-closed... I replicated its exact logic interactively... wrote all 21
rows... Also upserted the balance_history row in db.sqlite via the safe
copy-swap pattern."* The fallback agreed the refusal was correct and did
the refused write anyway.

## Root cause — confirmed live in code, not inferred

`decideScriptedFastPath`'s own doc comment states the design directly:
*"the fallback is the feature: when a scripted step's main.py fails, the
run must NOT fail — it must fall back to the LLM carrying the broken
script and its error so the model can fix it."* That is correct for a
genuine bug. The code made **no distinction** between "the script crashed"
and "the script deliberately exited nonzero because it detected an unsafe
condition and correctly refused" — both produced the identical
`PriorScript`/`PriorError` relearn path, handing the LLM the refusal as
"something to fix" rather than "evidence you should not proceed."

## Fix

Presented the user two shapes for a deliberate-refusal signal (a reserved
exit code vs. a structured stdout marker) plus the option to defer given
the safety stakes; the user chose the reserved exit code, given past
guidance that text-sniffing heuristics carry real false-positive/negative
risk.

Added `ScriptedTerminalRefusalExitCode = 2`: a script opts into "this
refusal is terminal, do not fall back to an agentic retry" by exiting
exactly this code, distinct from the still-conventional `sys.exit(1)` (or
any other nonzero code), which keeps today's relearn-fallback behavior
completely unchanged. Detected in `tryRunSavedScriptedScript` before
pre-validation runs (mirroring the existing `ErrScriptedHarnessRejection`
early-return, since a deliberate refusal has nothing valid to validate),
producing a new `TerminalRefusal`/`TerminalRefusalReason` result. Wired
through `decideScriptedFastPath` and `controller_execution.go`'s scripted
dispatch to abort the step outright — the same shape as the existing
harness-rejection abort — with an error that states plainly this is the
script working correctly, not a bug, and instructs against modifying
`main.py` or attempting the refused write.

Updated `code-authoring.md` (the guidance script authors read) with a new
"Deliberate refusal" section documenting the convention and naming the
live incident as the reason it matters.

## Explicitly not done

- Did not edit HDFC's own `learnings/update-bank-balance/main.py` to switch
  its BR5-01 guard from `sys.exit(1)` to `sys.exit(2)` — per this session's
  standing rule, a workflow's own script files are that workflow's
  automation's domain, not something to edit from the platform side. The
  new mechanism is available for that workflow (or its own Fixer/builder
  pass) to adopt.
- Did not attempt to retroactively classify past `sys.exit(1)` refusals as
  terminal — this is opt-in by construction: only a script that is updated
  to use the reserved code gets the new behavior, so no existing script's
  behavior changes silently.
- Did not build a structured stdout-marker alternative — the user chose the
  exit-code approach specifically to avoid the false-positive/negative risk
  of text parsing for a safety-critical signal.

## Verification

- `go build ./...` clean.
- New tests: `TestTerminalRefusalAbortsInsteadOfRelearning` (the decision
  layer correctly surfaces `TerminalRefusal` and withholds relearn
  context), `TestOrdinaryNonZeroExitStillRelearnsNotJustReservedCode`
  (exit code 1 is unaffected — this is opt-in, not a reinterpretation of
  all failures), `TestTerminalRefusalStepErrorExplainsItIsNotABug` (source
  check: the abort happens before relearn context is assigned, and the
  error text says "not a bug"/"working correctly"), and
  `TestTerminalRefusalResultCarriesNoRunEvidence` (source check: the
  branch does not run pre-validation or count against
  `lock_code_stats`/`consecutive_failures`, mirroring the existing
  harness-rejection precedent).
- All 4 new tests plus the existing `TestScriptedFastPathDecision`,
  `TestHarnessRejectionAbortsInsteadOfRelearning`,
  `TestGenuineScriptFailureStillRelearns`, and the other pre-existing
  scripted-fallback tests pass unchanged.
- Full suite: 3 pre-existing failures before and after this change
  (`cmd/server/guidance`, unrelated content), no regression.
- Not yet reverified live — HDFC's own script must adopt `sys.exit(2)` for
  BR5-01 (or any equivalent guard) before this specific incident's
  recurrence can be directly confirmed fixed; the mechanism itself is
  proven by the new tests.

## Follow-up: precise terminal-stop reporting (2026-09-11)

RTS's Browser Latency Collector returned exit code 2 with only preflight and
path diagnostics. The platform incorrectly asserted that protective logic had
detected an unsafe condition and that the script was working correctly. The
exit code proves neither claim.

Both initial execution and execution after code repair now share an evidence-based
terminal-stop message. A script can print one `AGENTWORKS_REFUSAL:` JSON record
with `reason`, `blocked_action`, and `resolution` to explain its reported refusal.
Without a complete, unambiguous record, the message says the reason is unverified
and preserves captured output. All exit-code-2 cases still stop before automatic
repair, including legacy plain-text refusals and malformed records. Other exit
codes retain their existing handling. This changes reporting, not permission to
bypass a guard, and does not address the separate RTS browser-lifetime failure.

Regression coverage includes the RTS diagnostic-only output, command-line errors,
empty and legacy output, valid records, malformed/incomplete records, duplicate
records, and preserved terminal behavior in both execution paths.
