[← goals / steps / config](index.md)

# PLAT-060 — Ops-owned config decisions did not carry their reason into the config

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | goals |
| Area | steps/config |
| Summary | `update_step_config` write path · `llm_ops_review` → Fixer handoff |

| Coordination | Value |
|---|---|
| Assigned agent | `Claude Code` |
| Ticket state | `implemented; runtime reverify deferred` |
| Last synchronized | `2026-08-09` |

- **Priority:** P2
- **Owner:** `update_step_config` write path · `llm_ops_review` → Fixer handoff
- **Related:** PLAT-059 (`lock_learnings_reason`) established the pattern.

## Problem

`llm_ops_review` already owns tier, model and execution-mode selection, is
explicitly read-only, and already produces *"current state, exact suggestion,
expected benefit, risk, and evidence"* for every recommendation — including a
strict tier-downgrade policy ending *"missing evidence means keep the tier."*

That rationale never reached the config:

1. Ops writes the evidence into the Pulse finding.
2. The Fixer applies it with `update_step_config(execution_tier=…)` — **no
   reason parameter**.
3. `planning/step_config.json`, which is what the *next* reviewer reads, records
   the change with no trace of why.
4. The Fixer had **no guidance at all** for this class of change: `execution_tier`,
   `declared_execution_mode`, and `tier` returned nothing from
   `pulse-review-fixer.md` or `pulse-fixer-practices.md`.

Two consequences were invisible at the call site:

- Setting `execution_tier` **permanently disables adaptive tiering** for the step
  (`shouldUseAdaptiveExecutionTiering`), so it stops promoting high→medium after
  3 stable runs.
- `execution_llm` **outranks tier entirely** and does not follow provider-profile
  updates.

## Implementation (2026-08-09)

Three validators alongside `validateLockLearningsChange`
(`controller_learning_helpers.go`), called from the `update_step_config` handler:

| Field | Reason | Consequence named in the rejection |
|---|---|---|
| `execution_tier` | `execution_tier_reason` (new) | disables adaptive tiering |
| `execution_llm` | `execution_llm_reason` (new) | outranks tier; ignores profile updates |
| `declared_execution_mode` | `declared_execution_mode_reason` (existed, now required) | scripted freezes behaviour into `main.py` |

- Reasons are read **before** their fields in the handler, so a reason supplied
  in the same call satisfies its own validator.
- Clearing a field clears its reason; clearing never requires one.
- Guidance: `ops-review.md` (write recommendations so they can be stored
  verbatim as the reason), `pulse-fixer-practices.md` (net-new "Applying an Ops
  config recommendation" playbook), `step-config.md`.
- UI: the `execution_tier` dropdown in `StepEditPanel.tsx` was removed, as the
  `lock_learnings` toggle was in PLAT-059. Current tier stays visible.

### The escape hatch is load-bearing

A required field invites a confabulated answer from an agent that has already
decided to act, and **an invented justification is harder to challenge later
than a missing one**. Every rejection therefore names the sanctioned
alternative: raise a decision with `create_human_input_request` and park the
finding `awaiting_user`. That path is already enforced —
`RecordPulseFindingDispositionsTx` rejects `awaiting_user` when the decision does
not exist or is no longer pending. Uncertainty is a legitimate terminal state.

### What was deliberately NOT gated

`learning_objective` and `knowledgebase_contribution` take **no** reason field.
They are meant to be refined continuously as evidence accumulates, and a
write-time gate would suppress exactly that iteration. Instead the Fixer's skill
playbook gained a yield check (step 5b) reading the already-recorded
`detection_history[].has_new_learning` in `.learning_metadata.json`: repeated
no-yield means sharpen the objective or drop to `learnings_access="read"`.
Reflection turns measured **20.1% of all LLM time** on Social Media's run, so an
objective that never yields is pure overhead — `ops-review.md` now names that a
first-class cost line.

## Regression tests

`ops_config_reason_test.go` — five tests: each field rejected without a reason,
accepted with one, not gated when unset; clearing clears the reason; reasons
survive `MergeAgentConfigFields`. Each rejection is asserted to name its hidden
consequence **and** `create_human_input_request`.

Full run reproduced the known 22-failure baseline exactly; no new failures.

## Acceptance / reverify

**Runtime reverify is deferred and cannot be claimed yet**: `llm_ops_review` is
currently disabled at the Gate for the core-system verification phase, so the
lens that supplies these reasons does not run. The enforcement is live; the
loop it serves resumes when that block is removed from `pulse-gate.md`.

## Follow-up: Architecture owns tier decisions (2026-09-11)

Removed runtime High-to-Medium promotion based on `description_hash_runs` and
all tier state updates from execution. Those counters were learning-reflection
records, not consecutive successful executions, and a description reset could
reuse the old count. Model selection now follows explicit model/tier/caller
settings; absent an override, execution defaults to High and evaluation to Medium.
Existing explicit pins are preserved. Legacy adaptive metadata remains historical
only and does not migrate into a persistent pin or affect execution.

Pulse Architecture now owns evidence-based tier/model proposals. It compares
actual execution/evaluation quality, retries, duration and workflow cost evidence,
proposes bounded trials through the existing impact/approval contract, and assesses
them at a checkpoint with rollback settings. It must respect user pins, avoid
per-run switching and not infer cheaper-model quality from a success count. QA
retains concrete provider failures and attribution defects. Tool descriptions,
configuration reason validation, reviewer guidance and step UI describe this same
ownership. No additional reviewer or reporting ledger was introduced.

Regression checks cover unchanged default and explicit-tier selection across
repeated calls, evaluation defaults, model/one-run override precedence, absence of
history-derived runtime overrides, reason requirements and the Architect contract.
