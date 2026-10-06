[← relays / plans-contracts](index.md)

# PLAT-454 — Steps and Relay guidance mention no platform stores they do not have

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | relays |
| Area | plans-contracts |
| Summary | fixed on `main`, not deployed: a step with no platform stores gets no database/KB/learnings text at all; the Relay Builder prompt and skill say how data moves instead of what is missing. |

| Coordination | Value |
|---|---|
| State | fixed on `main`; not deployed |
| Date | 2026-10-04 |
| Owner | plans-contracts |
| Related | PLAT-447 (Relays lose DB, KB and learnings), PLAT-441 |

## Source

A read-only review of PLAT-447's commit (a9a2a7245) found enforcement sound but
the generic step prompt still describing the database, knowledgebase and
learnings. The owner's rule for the fix: do not explain the absence ("there is no
database"); simply leave the topic out, because naming it keeps it in the model's
head.

## Done

- `step-system-prompts.md`: for a step with `DBAccess = none` the DB path row, the
  whole "persistent stores" section (soul, db, knowledgebase, learnings), the
  "output to the db" line and the DB example in the failure rule are omitted. A
  normal workflow step is unchanged.
- `BuildManagedWorkflowDBGuidance("none")` returns nothing; the message-sequence
  access note names no store for such a step.
- Relay Builder prompt and skill rewritten positively: data moves through INPUT,
  variables and step outputs; a user's own database or system is reached through a
  script tool or an MCP integration with secrets. The "Execution stores" section,
  the `platform_stores: false` explanation and the "no database / KB / learnings"
  sentences are gone.
- A false log line ("granted direct DB access but no DB_PATH") no longer fires for
  access `none`.
- Test: a rendered no-stores step prompt contains none of the store text; a normal
  step keeps it.

## Review points not changed

- Builder-side helper sessions (`setupWorkshopToolAgentSession`) are not gated for
  Relays. They are design-time helpers, and the Relay Builder's tool list has no DB
  tools, so there is no reachable path; left as is.
- A draft Relay on code layout 0 with saved scripts cannot run (API, test and
  Builder runs all require layout 1, and publishing does too), so the blocked
  `learnings/` folder does not matter in practice.
- `workflow.json` "not found" re-enabling stores is harmless: a real Relay always
  has a manifest.

## Register notes

[PLAT-454](plat-454.md), fixed on `main`, not deployed:
a step with no platform stores gets no database/KB/learnings text at all; the Relay
Builder prompt and skill say how data moves instead of what is missing.
