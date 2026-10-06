[← goals / pulse-governance](index.md)

# PLAT-072 — `external_action_required` has no exit path, so solved problems keep being re-reported as open

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | goals |
| Area | pulse-governance |
| Summary | P1 — stale findings actively mislead, and the count only grows |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `implemented` — closure mechanism, first sweep, and version stamping all shipped |
| Last synchronized | `2026-08-10` |

- **Priority:** P1 — stale findings actively mislead, and the count only grows
- **Owner:** Pulse finding lifecycle (`external_action_required` disposition)
- **Found on:** cross-workflow, while triaging the 81 open platform findings

## What this ticket was, and why it changed

It was originally filed as *"reused `iteration-0` makes per-run cost unattributable and overwrites run provenance"*, on the strength of six workflows independently reporting it: social-media (07-29), build-in-public (08-03), upwork (08-04), hetznerssh and rtslatency (08-05), linkedin (08-06). Six independent reports of the same defect is normally strong evidence.

**It was already fixed.** Commit `0f6519640`, *"Fix cost ledger run identity across rotation"*, **2026-08-06**, and comprehensively — not only the storage format but the readers and the rotation path:

```
cost_storage.go (server + orchestrator)   execution_id as immutable record identity
virtual-tools/tool_costs.go               the cost reader
evaluation_score_storage.go               the same fix for eval scores
controller_run_manager.go                 ArchiveRunCostPaths on rotation
review/ops-review.md                      the reviewer contract
execution_identity_cost_test.go           regression test
```

Rotation archives rather than deletes — social-media's `iteration-254` / `iteration-253` folders are those archives — and the code states the intent directly: *"Cost records keep the UUID as their immutable identity. Rotation only updates their human-readable archived path, so the next iteration-0 run cannot inherit historical spend."*

**Every one of the six findings is dated on or before that commit.** They are frozen descriptions of a solved problem.

## The real defect

`external_action_required` is a state with **no exit in either direction**:

- **It cannot close.** Nothing sweeps these findings when a platform fix ships. `resolve_run_concern` is limited by contract to *"acknowledgment or rejection"*, and Pulse is correctly read-only on platform matters.
- **It cannot reopen or self-correct.** The concern upsert flips a stale row back to `open` only for `resolved`, `awaiting_verification` and `awaiting_run`:
  ```sql
  status = CASE WHEN run_concerns.status IN (?, ?, ?) THEN ? ELSE run_concerns.status END
  ```
  `external_action_required` is deliberately absent, so re-observation leaves it untouched — the "suppresses unchanged rediscovery" property. But the same property means a finding whose problem was *fixed* is equally frozen.

So the board only grows: 13 findings from July, 68 from August, and the two newest both duplicate platform issues already known.

## Why this is worse than an untidy list

This ticket is its own evidence. Working with a full day of context on this codebase, six corroborating reports were enough to file a P1 for work completed four days earlier. The findings read as current because nothing about them says otherwise. The operator caught it — *"we already solved this before right, we had created a format for this, we maintain a uuid?"* — from memory, which is not a control that scales.

Every stale finding on that board is a live trap for the next reviewer, human or agent. And the compounding cost is real: a triage sweep attempted here matched only 11 of 81 by theme, and misattributed several of those, so bulk closure by pattern is not available either — the stale ones have to be read.

## Suggested fix

1. **Give the state an exit.** When a platform ticket reaches `implemented`, sweep findings whose `reason_code` / `external_owner` matches and dispose of them with the ticket id and commit as evidence. Not a bulk clear — an evidence-stamped closure, so the trail survives.
2. **Let it self-correct like every other state.** Adding `external_action_required` to the reopen list is a one-line change and makes closure safe: if the fix did not hold, the next observation reopens it. That safety net is exactly why closing these is low-risk, and it is the only reason bulk closure would be defensible at all.
3. **Record what a finding was reported against.** These carry no platform-version or commit context, so nothing distinguishes "still true" from "was true in July". A stamp at filing time would let a sweep answer this mechanically instead of by reading.

## Shipped (2026-08-10)

**`scripts/pulse_close_stale.py`** — the missing exit. `--list` shows the external-action board; `--close` requires a ticket, an evidence string and explicit fingerprints, dry-runs by default, and writes `resolved` with a stamped note naming the ticket and the fix.

Three design choices worth stating:

- **It does not decide what is stale.** A regex/theme mapping was tried first and matched 11 of 81 while misattributing several — a cost-attribution finding onto PLAT-070's run-outcome reconciliation, a retired-field finding onto the lock_learnings ticket. Closing on that basis would have produced resolution notes citing tickets that did not fix the finding. The judgement stays with the caller; the tool makes the closure safe, uniform and auditable.
- **Evidence is mandatory.** `--close` refuses to run without `--ticket` and `--evidence`, because a closure with no stated cause is precisely the unfalsifiable record this ticket exists to stop creating.
- **`resolved` is the safe direction and needs no Go change.** It is already in the concern upsert's reopen clause, so a wrongly closed finding flips back to `open` the next time it is observed. Closing wrongly self-corrects; leaving it stuck does not.

**First sweep applied: 81 → 75.** Six cost-attribution findings closed against `0f6519640`, spanning build-in-public, hetznerssh, linkedin, rtslatency, social-media and upwork.

Two were deliberately **left open** despite matching the same theme, which is the point of requiring judgement:

- social-media `5e248d9ec6e7244f` — *"Re-using `runs/iteration-0/default` deletes the previous occupant's artifacts"*. `0f6519640` archives cost and evaluation-score paths on rotation; whether workflow **artifacts** are archived rather than deleted was not verified, so this stays open.
- tectonicusadaytrading `d00ae7bdcb14a1ff` — learnings `_freshness.json` misattribution. Adjacent wording, different subsystem, untouched by that commit.

## Shipped: version stamping (2026-08-10)

The third piece — a finding now records the platform revision it was **first** observed against, so staleness stops being a memory test.

- `run_concerns.first_seen_platform_version`, written on INSERT only. The `ON CONFLICT` branch deliberately leaves it alone: a recurrence must not overwrite it with a newer revision, which would erase the signal.
- `PlatformVersion()` (`platform_version.go`), resolved once per process.
- Migration for existing databases; pre-existing rows keep an empty value, which is correct — their revision is genuinely unknown, and back-filling today's would assert something false.
- `pulse_close_stale.py --list` shows the revision, rendering unknown as `?` rather than blank.

**Go's own VCS stamping could not supply this, and that took verifying rather than assuming.** A `go.work` above the repo puts every build in workspace mode, which disables `-buildvcs` *silently* — a real `go build ./cmd/server` produced no `vcs.*` settings even with `-buildvcs=true`. The revision is therefore injected at link time by `run_server_with_logging.sh` via `-ldflags -X`, with build info kept as a fallback for other build modes. Verified by running the injected build, not by inspecting the binary: an initial `strings` check reported zero matches and was simply the wrong probe.

**Empty means unknown, never old.** A sweep that treated an unstamped finding as stale would close live findings — precisely the failure this mechanism exists to prevent. That contract is pinned by a test and stated at every layer.

Two defects were caught by this work rather than shipped:

- A bare `INSERT ... SELECT` in `migrateDuplicatePulseFindingIdentities` relied on positional column order and broke on the 15th column (`table run_concerns has 15 columns but 14 values were supplied`). Now names its target columns, so future columns cannot silently break it.
- The sweep tool's `--list` initially swallowed the missing-column error on un-migrated databases and reported **zero** findings for the whole board. It now falls back to the pre-migration query — reporting an empty board when 75 findings exist is a worse failure than the one the tool addresses.

## Acceptance

- A platform finding whose owning ticket is implemented can be closed with evidence, without a human remembering the fix happened.
- A wrongly-closed finding reopens on next observation rather than staying closed.
- The 81 currently on the board are triaged against the PLAT register, with stale ones closed and genuinely open ones left visible.
