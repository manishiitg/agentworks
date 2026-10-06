[← goals / human-decisions](index.md)

# PLAT-445 — Applying a decision in chat still pointed at a Fixer that no longer exists

| Field | Value |
|---|---|
| State | closed |
| Priority | - |
| Product | goals |
| Area | human-decisions |
| Summary | fixed on `main`, not deployed: every answered decision is applied by the Builder in chat, honoring any option picked; the dead pre-run Fixer/drain routing is removed. |

| Coordination | Value |
|---|---|
| State | fixed on `main`; not deployed |
| Date | 2026-10-04 |
| Owner | human-decisions |
| Related | PLAT-381 (decisions are applied only in the Builder chat), PLAT-093 |

## What happened

An answered decision with apply mode `targeted_fixer` and a non-`approve` answer
(`label_only`, a smaller option) was handed to the Builder chat with the old
"PRE-RUN DECISION DRAIN" text, which says "these are only direct_apply/no_change
decisions; repairs are routed to a dedicated targeted Fixer turn". The Builder read
the saved decision, saw it needed a repair, and left it unconsumed: "the saved
decision requires a targeted Fixer, while this drain permits only direct
application". No Fixer turn exists since PLAT-381 moved applying into the chat,
so the decision and its linked issue stayed open forever.

## Done

- `decisionApplyChatMessage` no longer builds on the scheduler's pre-run turns.
  Every structured or older decision gets one in-chat instruction: read it, honor
  the answer exactly (approve, a smaller option, a rejection, a deferral), apply it
  with the normal Builder tools, check it landed, and consume it. A structured
  decision's saved approved scope, required checks, post-run proof and linked issue
  are included and bound the repair.
- Removed the dead scheduler routing: `scheduledDecisionPreflightTurns`,
  `scheduledDecisionDrainTurn`, `scheduledTargetedDecisionFixerTurn`,
  `scheduledDecisionIsApproval`, and their tests.
- Tests: every apply mode produces the Builder message with no Fixer/drain wording;
  the reported `label_only` case carries scope, checks and issue; nothing to apply
  gives no message; an accepted user suggestion is still review, not edit authority.

## Left

- `scheduledWorkshopTurn.decisionDrain` and its handling in the schedule loop are
  now never set; they can be deleted in a cleanup.
- The decision that was stuck needs "Apply in chat" run again once the owner's app
  has this change.

## Register notes

[PLAT-445](plat-445.md), fixed on `main`, not
deployed: every answered decision is applied by the Builder in chat, honoring any
option picked; the dead pre-run Fixer/drain routing is removed.
