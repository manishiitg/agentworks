[← platform / learnings-knowledge](index.md)

# PLAT-289 — Substack research paths were fixed, but learning notes retained contradictory write/success contracts

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | goals |
| Area | learnings |
| Summary | The ten historical reports concern source steps told to write `db/research/` |

| Coordination | Value |
|---|---|
| State | Workflow learning corrections applied; retained artifacts verified; file-transport regression passes |
| Date | 2026-09-05 |
| Related | PLAT-175, PLAT-221, PLAT-257; research group in the full remaining-report audit |

## Actual state

The ten historical reports concern source steps told to write `db/research/`
while their managed-agent filesystem grant only permits `db/assets/` below
`db/`. Some attempted a per-date SQL table as a workaround; ordinary mutation
tools correctly refuse DDL. Broadening `db/` would expose raw SQLite and is not
the repair.

Before this pass, all four source descriptions and their validation path regexes
already used the final `db/assets/research/YYYY-MM-DD-<source>.json` location.
The parent and `db/README.md` agreed. No promotion into `db/research/` is required.

Three live learning instructions still contradicted that corrected contract:

- The shared SKILL index called legacy `db/research/` canonical and described a
  deprecated flat mirror filename workaround.
- Hacker News claimed an orchestrator would promote the asset into the old path.
- Reddit directed the step to populate success literals after a denied write,
  assuming downstream tooling would materialize the packet, and named retired
  `record_run_concern`.

Corrected only these three statements in the workflow's learning files. Reddit
now fails honestly on write/readback failure; success literals require actual
readback validation. No plan, schedule, permissions, database schema, or business
packet was changed. The retired CLI radar and separate growth/drafts groups are
outside this repair.

## Retained run verification (read-only)

The four 2026-09-05 durable packets parse, contain the expected source, and have
matching `finding_count == len(findings)`:

| Source | Findings |
|---|---:|
| Reddit | 15 |
| X/Twitter | 5 |
| Hacker News | 29 |
| Web search | 8 |

All four forward outputs' findings match their durable packets exactly; receipt
paths/sources/counts agree. The parent `research_summary.json` lists those four
paths and totals **57** findings. File SHA-256 values and the checked plan hash
are preserved in each SQLite closure event. This verifies retained files, not
the truth/freshness of every external news story, and does not claim we launched
a fresh producing run.

## Regression

`TestMessageSequenceResearchPacketsPersistInsideGrantedAssets` constructs the real
message-sequence guard, configures the managed DB session, and writes/reads JSON
through the guarded workspace client and production HTTP document handlers in a
temporary docs root. Each source packet is genuinely persisted and parsed.
Writes to legacy `db/research/`, `db/db.sqlite`, and its WAL sidecar are rejected
without creating those files.

Focused message-sequence, DB-session, spill-folder and path tests pass.
The new test exercises the file API, not Linux Landlock or macOS shell execution.
No platform permission implementation was changed.

## Exact internal closures

PUL-AB711AC1, PUL-01F790F3, PUL-5903292F, PUL-137C5EDF, PUL-22656D6C,
PUL-F7B23729, PUL-9CE79401, PUL-32C03E39, PUL-B8FC2F51, PUL-224A1885.

Resolved these superseded-path reports in SQLite and read-back verified, with prior concern/detail records retained
in `platform_tracking_resolved` event metadata. This is not a claim that generic
JSON regex validation independently checks referenced file existence: it does
not. Current file presence was checked directly and the workflow's explicit
readback contract remains necessary. Historical missing packets are not fabricated
or backfilled. Learning/test changes remain uncommitted.


## 2026-09-09 local Pulse audit follow-up

The local Substack learning correction remains applied. Related contradictory
guidance in other workflows belongs to builder/change-contract prevention under
[PLAT-257](plat-257.md), not reopening this already-corrected Substack issue
without a fresh reproduction.
