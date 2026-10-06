[← goals / plans-contracts](index.md)

# PLAT-212 — HDFC's recurring workflow-upgrade preflight abort is already fixed by PLAT-096's stamp-fencing work

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P3 |
| Product | goals |
| Area | plans-contracts |
| Summary | P3 — no live defect. |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `resolved` — correction/closure record only, no code change; already fixed the day after the last recurrence, never re-verified |
| Last synchronized | `2026-08-29` |

- **Priority:** P3 — no live defect. A critical-severity finding that
  correctly described a real, repeated, workflow-destroying bug when
  filed, but never got re-checked against the fix that landed for it.
- **Owner:** N/A — no code change made or needed by this ticket.
- **Related:** [PLAT-096](plat-096.md) (the fix), [PLAT-098](plat-098.md)
  (manual recovery visibility, same subsystem). `harness:workflow-upgrade-preflight:contradictory-version-and-schema-target`
  (`Workflow/HDFC-Personal-Accounts`, critical, first seen 2026-08-04,
  RECURRED as of 2026-08-11) — the finding this closes.

## The finding

Four separate occurrences (2026-08-02, 08-03, 08-04, and a RECURRED
observation on 2026-08-11) where the workflow-upgrade preflight aborted an
entire scheduled run — on 2026-08-11, all 8 groups, zero steps executed —
with *"did not stamp required version '1.0.25' (found '1.0.23', failure
1/3 consecutive)"*. Recovery required stamping `workflow.json` to the
demanded version out of band each time. The finding's own 2026-08-05
"symptom cleared, defect unexercised" verdict was explicitly reopened by
this 08-11 recurrence, satisfying its own stated reopen condition.

## Already fixed — confirmed via git history and live evidence, not assumed

PLAT-096's stamp-fencing fix (`pkg/contractupgrade`, commit `79ba90168`,
**2026-08-12**) is exactly the mechanism this recurrence matches: an
unfenced `set_workflow_contract_version` write reachable from any turn in a
scheduled session, causing a scheduled preflight to see a manifest version
it didn't expect. PLAT-096's own text independently confirms HDFC by name:
*"The same rung completed on three other workflows the night before —
ICICI-BANK-PARSING 22:20→22:24, **HDFC-Personal-Accounts 22:39→22:42**,
ICICI-BANK-PARSING-v2 23:13→23:16 — all retired `improve.html` and reached
`1.0.25`."*

Confirmed independently against this workflow's own live evidence rather
than trusting that citation alone:

- `workspace-docs/Workflow/HDFC-Personal-Accounts/schedule-runs.json`,
  sorted by `started_at`: the 2026-08-11T17:09 abort is the **last**
  contract-upgrade-preflight error in the file. The very next run
  (2026-08-12T02:23:52, after the fix landed the same morning) succeeded.
  No further preflight-abort errors appear through the file's latest entry
  (2026-08-18, an unrelated `interrupted: server restarted`).
- `workspace-docs/Workflow/HDFC-Personal-Accounts/workflow.json`'s current
  `version` is `1.0.26` — one rung *past* the `1.0.25` that was blocked,
  confirming further contract bumps have completed successfully since.

## Why this stayed open

Same pattern as PLAT-191/195/201/202 this session: a harness finding
correctly described a real defect, a fix landed for it, and nothing
re-verified the finding against the fixed code before this pass. The fifth
instance of this exact pattern found in one review sweep — now clearly a
recurring gap in this platform's own finding lifecycle (nothing
automatically re-checks an open harness finding against current code), not
a coincidence.

## Explicitly not done

- No code change — PLAT-096, already shipped and independently re-verified
  here, covers this.
- Did not re-derive PLAT-096's own extensively-documented root cause
  analysis (stamp fencing, single delivery path, unattended-turn handling,
  Pulse-skip-on-block) — cited and confirmed its outcome against this
  specific workflow's evidence rather than repeating the investigation.
- Did not investigate PLAT-096's own still-open follow-ups (minimal-context
  upgrade turns, inverting the stamp, confida-login's self-reinforcing
  refusal) — out of scope for this closure record, which is specifically
  about HDFC's recurrence.

## Verification

- Direct read of `schedule-runs.json` and `workflow.json` inside
  `Workflow/HDFC-Personal-Accounts`, sorted and cross-checked against the
  finding's own cited run IDs and timestamps.
- `git log` confirms the fix commit (`79ba90168`, 2026-08-12) postdates
  every cited recurrence and predates the next successful run.
- No code changed by this ticket — nothing to build or test.
