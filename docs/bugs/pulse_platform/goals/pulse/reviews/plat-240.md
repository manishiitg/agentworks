[← goals / pulse / reviews](index.md)

## Internal reconciliation — 2026-09-05

PUL-517B83AD and PUL-0B679A3D: additional historical verification-endpoint/marker reports superseded by the current tested disposition lifecycle (see PLAT-198).

Resolved in SQLite for internal tracking with previous concern/detail records
preserved in resolution events. Source/tests verified; deployed replay and
historical business/module-result repair are not claimed. Full mapping:
[remaining-report audit](../../../../../audits/platform-open-report-reconciliation-2026-09-05.md).

## SQLite backlog reconciled — 2026-09-05

Resolved PUL-57CFE46C, PUL-B1E9F611 in the corresponding workflow databases after checking
the existing implementation and passing focused regression tests. Full evidence
and the complete remaining inventory: [reconciliation audit](../../../../../audits/platform-backlog-reconciliation-2026-09-05.md).
This is internal tracking closure, not a claim of a new deployed end-to-end run.
Previous SQLite records are retained in audit events; unrelated findings remain
open. No business data or historical schedule outcome was rewritten.

# PLAT-240 — `record_pulse_verification`'s silent-overwrite bug: tool removed entirely, already resolved

| Field | Value |
|---|---|
| State | closed |
| Priority | - |
| Product | goals |
| Area | pulse/reviews |
| Summary | harness_issue, severity high, classification |

| Coordination | Value |
|---|---|
| Assigned agent | Claude Code |
| Ticket state | `resolved` |
| Last synchronized | `2026-08-29` |

- **Priority:** harness_issue, severity high, classification
  `typed_state_persistence`.
- **Findings:** Twitter/social-media `PUL-3880D006` — `record_pulse_verification`
  reported `status=recorded` on repeated calls for the same attempt with a
  different verdict/evidence, but `pulse_fix_verifications` retained only
  the older row. Evidence cited the table's `UNIQUE(attempt_id, fingerprint,
  check_text)` constraint and noted the tool exposed no `check_text`
  parameter to create a distinct judgment — implying every repeat call for
  the same attempt collided on an identical (likely empty/fixed)
  `check_text`, and whatever the write path did on that collision silently
  discarded the new verdict rather than updating it.

## Already resolved: the tool itself no longer exists

`git log -S "record_pulse_verification"` shows the standalone
`record_pulse_verification` tool was removed entirely in commit `d9223aa61`
("Simplify Pulse review and repair flow", **2026-08-29 00:57 +0530** — a
few hours before this session began). The removed diff deleted an entire
tool-authority switch block that named it alongside several other
now-obsolete `record_pulse_*` tools
(`pulse_write_authority_test.go` lost ~60 lines in the same commit). `grep
-rn "record_pulse_verification"` across `agent_go` today finds zero
matches — the tool name, its handler, and its authority wiring are all
gone. This is part of the same ongoing Pulse tool-surface consolidation
already tracked in [PLAT-220](plat-220.md).

## The replacement path does not have this bug

Verification evidence is now recorded exclusively through
`record_pulse_result`'s `finding_dispositions[].verification[]` array
(`pulse_finding_lifecycle.go` ~line 1918), which uses a real UPSERT:

```sql
INSERT INTO pulse_fix_verifications
  (attempt_id, fingerprint, check_text, verdict, expected, observed, evidence_json, verified_at)
  VALUES (?, ?, ?, ?, ?, ?, ?, ?)
  ON CONFLICT(attempt_id, fingerprint, check_text) DO UPDATE SET
    verdict=excluded.verdict, expected=excluded.expected, observed=excluded.observed,
    evidence_json=excluded.evidence_json, verified_at=excluded.verified_at
```

`check_text` here is `verification.Check` — a real, caller-supplied string
naming what was checked (required by schema, non-empty) — not a fixed or
absent value. A repeated call for the same `(attempt_id, fingerprint,
check_text)` correctly overwrites `verdict`/`expected`/`observed`/
`evidence_json`/`verified_at` via `DO UPDATE`, rather than silently
preserving the old row. The exact mechanism the finding describes (a
missing `check_text` parameter forcing every call onto one collision-prone
key with no real update) cannot recur through this path.

## Verification

Confirmed via `git log -S`/`git show` against actual commit history, and
by reading the current `RecordPulseFindingDispositionsTx` UPSERT logic
directly — not inferred from the finding text alone. No code changed this
session; the standalone tool this finding describes has been deleted, and
its replacement was already correct when checked.

## Reverify

Not applicable to this session's work. If a future run somehow observes
verification evidence being silently dropped through
`record_pulse_result`'s `finding_dispositions[].verification[]` path,
that would be a new, different bug in the UPSERT logic above — not a
recurrence of this finding, whose exact described mechanism (the tool it
names) no longer exists.
