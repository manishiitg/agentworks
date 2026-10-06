[← goals / pulse / reviews](index.md)

# PLAT-199 — Pulse paid for a fresh Fixer context after Technical Review and stretched one repair across too many cycles

| Field | Value |
|---|---|
| State | open |
| Priority | P0 |
| Product | goals |
| Area | pulse/reviews |
| Summary | P0 — review, approval, Fixer selection, and later verification |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | `implementation_in_progress` — live Pulse reverify exposed that the receipt-unlock phase split was brittle and unnecessary; it is now one retained Review+Fix task, with deployment reverify remaining |
| Last synchronized | `2026-08-29` |

- **Priority:** P0 — review, approval, Fixer selection, and later verification
  repeatedly turned one small repair into several expensive Pulse passes.
- **Owner:** scheduled Pulse dispatch, background message-sequence runtime,
  reviewer receipt identity, tool authorization, and Review→Fix guidance.
- **Related:** [PLAT-138](plat-138.md), [PLAT-155](../findings/plat-155.md),
  [PLAT-163](plat-163.md), and [PLAT-198](../findings/plat-198.md).

## Evidence and decision

The previous scheduler launched a retained Technical Review sequence, returned
to the parent, then launched a fresh independent Fixer. That clean-room boundary
prevented self-approval, but it also discarded the reviewer's selected evidence,
route context, and repair reasoning. The Fixer paid to reload them and could
select a different queue item; approved prompt cleanup therefore remained open
across repeated Review/Fixer passes.

Pulse now keeps review and bounded repair in one retained technical task:

```text
review + bounded safe repair + proportional proof
  → typed findings/focus + repair disposition + completed child receipt
  → terminal module result
```

This supersedes only the “fresh independent Fixer conversation” decision in
PLAT-138/155/163. Their still-valid boundaries remain:

- raw workflow observations are not repairable issues until Technical Review
  classifies/promotes them;
- the review receipt is durable and separate from repair outcome;
- Strategic Review remains a separate read-only sequence;
- high-risk, ambiguous, public-action, or operator-decision work is not silently
  repaired merely because the technical child retained context.

## Backend enforcement

1. `run_in_background` retains the exact child-session review receipt contract
   through `required_pulse_review_modules=["technical_review"]`.
2. One retained Technical Maintenance task reviews, selects only a bounded safe
   repair when warranted, applies it, proportionally verifies it, and persists
   typed findings, dispositions, terminal module result, and receipt before it
   ends. It does not need an artificial message-sequence boundary.
3. The receipt remains required after the task, so incomplete work cannot be
   presented as successful. It is evidence of completion, not a permission
   switch.
4. Existing durable human-decision, tool, folder, and mutation contracts still
   control what the agent may change. Risky, ambiguous, public-action, and
   approval-gated work remains out of scope for automatic repair.
5. Review rows now keep two identities: `pulse_run_id` correlates the parent
   Gate pass, while `review_run_id` is the exact child tool session used by the
   receipt and review lookup.

## Live verification regression and simplification — 2026-08-29

The scheduled run `schedule-manual--manual-p_1787943151546667000` reached the
Technical Maintenance child but ended with:

```text
background Pulse Technical Maintenance ended before its completed review
receipt unlocked the repair phase
```

This was not a missing-receipt/session-identity defect (PLAT-196's separate
diagnostic family). The child never had a chance to write a receipt. In
code-execution mode it reaches HTTP-backed Pulse tools through the native
`execute_shell_command` bridge, but the read-only phase gate rejected that
transport with HTTP 403 before its request ran.

That exposed a deeper design problem: the receipt was required to unlock repair
even though the same retained agent already had all review context and could
safely perform a bounded repair. The two-turn permission switch added latency,
failure modes, and bridge-specific exceptions without adding useful separation.
No plan, workflow, issue, or repair state changed during the failed run.

The replacement removes `pulse_phase_contract`, the per-session read-only
phase map, the HTTP phase gate, the inter-turn receipt observer, and the
required repair follow-up message. Technical Maintenance now performs its
bounded review and repair in one retained task, then writes the same durable
receipt and terminal result before completion. The receipt requirement remains
at task completion, preserving truthful lifecycle and audit evidence without
creating a permission deadlock.

Targeted test:

```text
go test ./pkg/orchestrator/agents/workflow/step_based_workflow \
  -run TestRunInBackgroundPassesBuilderSkillSnapshotToBothAgentKinds -count=1
```

passes alongside the scheduler and manual-command contract tests. A fresh
scheduled or manual retained Review+Fix run after deployment must prove this
end-to-end before the ticket returns to `implemented`.

## Acceptance

1. A due Technical Review and its bounded repair use one background executor,
   one conversation history, and one MCP session.
2. The same task may apply only a bounded safe repair while it holds the review
   context; it records findings, proof, outcome, and receipt before ending.
3. A task that ends without its receipt fails rather than claiming review or
   repair completion.
4. No extra sequence turn, shell transport exception, or receipt-unlock state
   is required for a normal retained Review+Fix run.
5. The task either applies/verifies a bounded canonical repair or records a
   truthful no-safe-repair technical result and completed receipt.
6. The scheduler no longer launches a separate Fixer stage or fresh Fixer
   background agent.
7. Strategic Review remains independently receipted and cannot repair workflow
   implementation.
8. The parent scheduler advances to finalization only after due module results
   and reviewer receipts are both durable.
9. Manual `/pulse-review`, `/plan-prompt-bloat`, and every focused
   `/pulse-review-*` alias use the same retained Review+Fix task;
   store aliases select `store_integrity` plus their knowledgebase, learnings,
   or database lens. The explicit `/pulse-fixer` command remains available for
   repair-only recovery against an already reviewed queue.

## Decision history

- **2026-08-28:** Replace the fresh Fixer child with one retained Technical
  Maintenance message sequence. This superseded the conversation-isolation
  mechanism, while initially retaining a backend receipt-gated tool transition.
- **2026-08-28:** Focused scheduler, background-sequence, typed-receipt, and
  phase-authorization tests pass. Full package suites still contain unrelated
  pre-existing failures tracked outside this ticket; live scheduled-Pulse
  re-verification remains.
- **2026-08-28:** Extended the retained Review+Fix contract to all manual
  Pulse Review slash aliases. A manual child uses its own exact tool-session
  identity as `pulse_run_id`, keeping worklist, receipt, findings, and repair
  outcome in one correlation scope.
- **2026-08-29:** A live 403 bridge failure showed the receipt-gated transition
  was an unnecessary dependency between one agent's review and repair. Remove
  the phase contract and keep the durable receipt as a required end-of-task
  record instead.


## 2026-09-09 local Pulse audit follow-up

The local audit found repetitive technical attention and sparse strategic
execution. Keep observations separate from accepted issues; a platform-owned
handoff with no available workflow repair must not become a new workflow repair
on every tick. [PLAT-303](plat-303.md) tracks exception-driven technical selection,
protected strategic completion, and remaining schedule independence. Existing
lifecycle/retained-task implementation is not proof that this broader policy is
fully implemented.
