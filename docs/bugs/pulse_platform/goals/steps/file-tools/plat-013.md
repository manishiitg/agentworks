[← goals / steps / file-tools](index.md)

# PLAT-013 — legacy regular steps lacked a semantics-preserving edit path

| Field | Value |
|---|---|
| State | open |
| Priority | P1 |
| Product | goals |
| Area | steps/file-tools |
| Summary | `HARNESS-LEGACY-REGULAR-DESC-EDIT` |

| Coordination | Value |
|---|---|
| Assigned agent | `Unassigned` |
| Ticket state | `runtime_verified` |
| Last synchronized | `2026-08-04` |

> Claim this ticket in this file before implementation. During active work,
> update this fragment rather than the shared index; synchronize the index
> once at handoff, review, or completion.


- **Priority:** P1
- **Owner:** plan/step editing API
- **Source finding:** `HARNESS-LEGACY-REGULAR-DESC-EDIT`
- **Source database:** `Workflow/rtslatency/db/db.sqlite`
- **Current state:** **reverify**. The editing path has since been changed, but
  the stored platform finding has not observed a successful repair on both RTS
  voice collector steps.
- **Acceptance:** edit the description of a fixture and one real legacy
  agentic `regular` step without changing its type, schedule, model, tools, or
  other persisted fields; read it back and run plan validation.

