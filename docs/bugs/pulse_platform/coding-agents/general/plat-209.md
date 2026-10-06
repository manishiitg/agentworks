[← coding-agents / general](index.md)

# PLAT-209 — a "35-minute hung LLM call" was host machine sleep, not a workflow or platform timeout defect

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | coding-agents |
| Area | general |
| Summary | P4 — external/informational. |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `resolved` — informational closure record, no code change |
| Last synchronized | `2026-08-29` |

- **Priority:** P4 — external/informational. The finding's own conclusion is
  that no bounded-timeout or workflow-side change is warranted for the
  incident it investigated.
- **Owner:** N/A — host power-state interruption, not a platform code
  boundary.
- **Related:** `host:machine-sleep:llm-session-continuity` (confida-login,
  low), the finding this closes. `PUL-048B1B72` (an abort-time
  `qa_cycles` RUNNING-state terminalization gap the finding names as *"IS
  fixable"*) is a separate, workflow-local (not platform) concern per this
  register's own scoping rule — not tracked in
  `platform_harness_issues` under any key, out of scope for this ticket
  and this register.

## The finding, correcting its own premise

*"Informational/external, and it corrects the premise: the 2026-08-18
abort was NOT a 35-minute hung LLM call. The LLM call took 5.4 minutes; the
other 30.2 minutes are untracked wall-clock while the host machine was
asleep. No bounded-timeout plan change is warranted."*

The finding's own evidence is precise and self-sufficient: the failing
step's execution-attempt record shows `llm_duration_ms=323792` (5.4
minutes, tracked) against `untracked_duration_ms=1810532` (30.2 minutes,
untracked) — 85% of the apparent step duration is suspended wall-clock
while the host was asleep, not execution time. The provider's own error
text (*"Your computer went to sleep mid-response"*) and a prior commit
(`872edfb`, *"Transient host-sleep failure, not a workflow defect"*)
corroborate the same conclusion independently.

## Disposition

This is a diagnosis that disproves its own premise, not an open question.
The finding explicitly recommends **no** timeout-dimension change: *"No
workflow-side defect in the timeout dimension, so no plan change should be
forced for it."* It separately notes (without claiming as part of this
finding's own scope) two real residues, both explicitly filed or
identifiable elsewhere:

1. `qa_cycles` staying `RUNNING` after an abort with no terminalization
   path — the finding names this `PUL-048B1B72` and calls it fixable, but
   it is not present in `platform_harness_issues` under any target key.
   Per this register's own scoping rule (*"a finding belongs here only
   when the failed boundary is owned by the workflow runtime... not by the
   workflow plan or its data"*), an abort-time `qa_cycles` table
   terminalization gap reads as workflow-local data-lifecycle ownership,
   not a platform boundary — left for the workflow's own Pulse system to
   track, not migrated into this register speculatively.
2. No fallback model configured on any role — a genuine single point of
   failure, but the finding itself notes it would not have helped in this
   specific incident (*"host suspension affects every provider on that host
   equally"*), and configuring fallback models is a workflow-authoring
   choice, not a platform defect.

## Explicitly not done

- No code change — the finding's own conclusion is that none is warranted
  for the timeout/hang question it investigated.
- Did not chase `PUL-048B1B72` into this register — confirmed it is not
  tracked as a platform-level harness issue under any key, and its shape
  (a workflow's own DB table lifecycle) reads as workflow-owned per this
  register's scoping rule, not platform-owned.

## Verification

- Confirmed via the finding's own cited execution-attempt record and
  provider error text; no independent re-verification needed beyond
  reading the evidence it already presents.
