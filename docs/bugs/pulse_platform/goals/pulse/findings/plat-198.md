[← goals / pulse / findings](index.md)

## Internal reconciliation — 2026-09-05

PUL-0B679A3D and PUL-517B83AD: old verification marker/allowlist and record_pulse_verification pending-attempt flow are retired; current applied-fix, no-attempt history and real MCP applied-fix tests pass.

Resolved in SQLite for internal tracking with previous concern/detail records
preserved in resolution events. Source/tests verified; deployed replay and
historical business/module-result repair are not claimed. Full mapping:
[remaining-report audit](../../../../../audits/platform-open-report-reconciliation-2026-09-05.md).

# PLAT-198 — Pulse kept applied fixes active behind a second verification queue and exposed text-derived identity

| Field | Value |
|---|---|
| State | closed |
| Priority | P0 |
| Product | goals |
| Area | pulse/findings |
| Summary | P0 — applied repairs remained active for days and repeated review |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | `implementation_in_progress` — close-on-applied policy, legacy reconciliation, closed-issue semantic index, merged-alias routing, issue-ID-only API/UI projection, and focused tests implemented; live migration verification pending |
| Last synchronized | `2026-08-28` |

- **Priority:** P0 — applied repairs remained active for days and repeated review
  and Fixer cycles, so the queue measured verification debt rather than unfinished
  repair work.
- **Owner:** Pulse issue registry, finding lifecycle, reviewer/Fixer contracts,
  and Pulse UI identity.

## Evidence and root cause

Social Media retained applied repairs as `awaiting_verification` and required a
later reviewer or producing run to close them. The same workflow contained
active rows with passing proof and already-merged aliases. The lifecycle was:

```text
fix applied → awaiting verification → another review/run → close
```

This differs from the platform issue register, where one canonical issue is
complete when its assigned repair is implemented and is reopened only when new
evidence proves recurrence. Pulse also exposed its text-derived fingerprint in
the REST/UI lifecycle model even though agents already used public `issue_id`.

## Decision and implementation

Pulse now follows the issue-register lifecycle:

```text
fix applied → close → semantically same concern recurs → reopen issue_id
```

1. `changed_unverified` means the Fixer successfully applied changed files but
   had no stronger immediate proof. It now resolves the issue and records a
   `fix_applied` event. A failed immediate check remains a failed repair.
2. An idempotent database migration closes legacy `awaiting_verification` rows
   backed by a real `changed_unverified` attempt with changed files and marks
   the attempt `applied`.
3. The existing concern recorder reopens a resolved canonical row when the same
   issue recurs. If an old merged alias recurs, its evidence is redirected to
   the canonical issue and the retired alias remains closed.
4. Reviewers inspect the compact issue index and semantically compare current
   evidence with existing issue text/history. They pass the existing
   `issue_id` for the same root cause and omit it only for a genuinely distinct
   issue.
5. `issue_id` is now generated and stored when a concern is first recorded;
   it is never derived from its wording or the legacy key. It is the sole
   product, UI, and reviewer/Fixer identity. Fingerprints are removed from Go
   JSON projections, TypeScript API types, React keys, and reviewer/Fixer
   instructions. Existing SQLite fingerprint columns remain private legacy join
   keys only while companion lifecycle tables are migrated.
6. No dedicated workflow run or later reviewer verification is scheduled only
   to close an applied repair.

### Semantic issue index

The compact Pulse backlog now supplies reviewers with both active canonical
issues and concise summaries of closed canonical issues. Before creating a
finding, a reviewer compares the new root-cause evidence against that whole
index: it supplies the matching `issue_id` to reopen a closed issue, or omits
the ID only for a genuinely new root cause. An occurrence against a merged
legacy alias is recorded on its canonical issue; the alias stays retired.

### Workflow-upgrade migration scope

Workflow-contract v1.0.32 calls a typed, idempotent reconciliation once per
workflow, reads the resulting compact register, and stamps only after both
succeed. For Social Media it is expected to close five legacy applied
verification-debt rows and retire two reopened merged aliases. This is
deliberately narrower than the earlier broad stale-state count: only repairs
with recorded changed files and no failed immediate check are auto-closed.
On-access reconciliation remains a recovery path for restored or previously
missed databases.

## Acceptance

1. An applied Fixer change closes its issue in the same lifecycle transaction.
2. A failed immediate check cannot close the issue.
3. A later semantically matching observation that supplies the existing
   `issue_id` appends evidence and reopens that canonical issue rather than
   creating another issue.
4. Existing applied `awaiting_verification` rows reconcile to resolved without
   deleting their attempt, evidence, or event history.
5. Pulse REST/UI/tool projections use `issue_id`; no fingerprint is exposed or
   required by an agent or frontend component.
6. A newly created issue receives a stored random `PUL-…` address, and a text
   change cannot change that address.
7. `awaiting_run` remains available only when no repair was applied and the
   evidence genuinely does not exist.
8. The v1.0.32 workflow upgrade records its reconciliation result, preserves
   every fix/event, and leaves human/platform-owned issues open.
9. The compact backlog provides closed canonical issue summaries for semantic
   reuse, and a recurrence of a merged alias updates the canonical issue rather
   than reopening the alias.
