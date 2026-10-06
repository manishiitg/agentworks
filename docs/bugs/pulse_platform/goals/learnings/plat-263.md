[← goals / learnings](index.md)

# PLAT-263 — Retire the redundant per-step learning lock

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | goals |
| Area | learnings |
| Summary | product simplification, severity medium. |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | `implemented` |
| Last synchronized | `2026-08-31` |

- **Priority:** product simplification, severity medium.
- **Related:** [PLAT-055](plat-055.md), [PLAT-059](plat-059.md),
  [PLAT-258](../pulse/reviews/plat-258.md)), [PLAT-260](../pulse/lifecycle/plat-260.md)), [PLAT-265](../../brain/learnings-knowledge/plat-265.md).

## Problem

The platform represented “this step may read shared learnings but must not
write them” twice: `learnings_access="read"` and
`lock_learnings=true` layered over read-write access. The overlap produced a
second reason field, a lock audit/version-upgrade turn, Builder and HTTP
mutation paths, runtime bootstrap exceptions, deletion-time unlocking, and a
separate Pulse lifecycle proposal. Those paths could disagree even though the
desired state was already expressible directly.

## Implemented contract

- `learnings_access` is the only learning permission:
  - `read` consumes shared guidance without a contribution turn;
  - `read-write` also contributes and requires a concrete
    `learning_objective`;
  - `none` neither reads nor writes.
- `lock_code` remains independent and continues to protect proven scripted
  `main.py` artifacts.
- Pulse and plan-drift review judge whether read-write access is still useful;
  they do not create or manage a second learning lock.

## Migration

`AgentConfigs` retains the two old JSON names only as migration inputs. On
step-config read/repair:

- legacy `lock_learnings=true` becomes `learnings_access="read"`;
- an explicit `learnings_access="none"` remains `none` (migration never widens
  access);
- legacy false locks and all legacy reasons are removed;
- normal persistence writes no retired keys.

The HTTP config endpoints reject new attempts to set either retired field and
point callers to `learnings_access="read"`.

## Removed surfaces

- runtime lock checks, bootstrap exception, metadata projection, and background
  completion hints;
- Builder tool schema/handler/status prompts and the frontend lock toggle;
- delete-learning “unlock” requests;
- the obsolete 1.0.22 learning-lock audit upgrade turn;
- current guidance that recommended lock/unlock workflows.

## Verification

- Migration tests cover true → read, explicit none preservation, false-lock
  removal, and clean serialized JSON.
- Runtime tests prove read-only access suppresses message-sequence learning
  turns.
- Tool-schema and HTTP tests ensure retired fields cannot be newly authored.
- Focused Go tests, the full Go build, frontend typecheck/production build,
  and repository-search checks passed before handoff.
