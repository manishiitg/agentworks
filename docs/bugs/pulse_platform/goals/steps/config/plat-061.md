[← goals / steps / config](index.md)

# PLAT-061 — step_config field audit: dead field, orphans, phantom clears, incomplete merge

| Field | Value |
|---|---|
| State | closed |
| Priority | P2 |
| Product | goals |
| Area | steps/config |
| Summary | `AgentConfigs` / `step_config.json` surface |

| Coordination | Value |
|---|---|
| Assigned agent | `Claude Code` |
| Ticket state | `implemented` (all of E1–E5) |
| Last synchronized | `2026-08-09` |

- **Priority:** P2
- **Owner:** `AgentConfigs` / `step_config.json` surface
- **Origin:** audit of all 28 `AgentConfigs` fields while designing PLAT-060,
  prompted by "review all fields we have in step config… if they are relevant or
  we have extra and legacy".

Each field was checked for three things: a runtime consumer, a setter, and
documentation. 19 were healthy.

## E1 — `db_access` removed ✅

2026-09-05 reconciliation: Instagram `PUL-51D2EC0F` is closed as superseded by
this intentional field retirement, not as a new permission repair. Its complaint
about losing stored `db_access` does not justify restoring an inert grant.
`TestMergeAgentConfigFieldsCoversEveryField` passes for the supported field set.
The previous finding is preserved in its SQLite resolution event; no arbitrary
unknown-field preservation or deployed workflow verification is claimed.

`resolveDBAccess(_ *AgentConfigs)` **ignored its argument entirely** and always
returned read-write; every read path discarded the field. Yet it kept a live
setter, documentation, and an enum `["read","read-write"]` that invited agents to
"restrict" a step the runtime always granted full access to.

This is the **`global_skill_objective` shape that caused the 1.0.21 deadlock** —
persisted, merged, documented, agent-settable, zero effect — but worse, because
that one was inert while this one solicited writes.

Removed: struct field, clear case, known-clear list, tool-schema property and
handler, and the migration path in `message_sequence_code_migration.go` that
derived it from whether a migrated item wrote to the DB (with its now-orphaned
`migrationCodeItemWritesDB` helper). Docs updated.

**Parse safety was the gate, not a formality**: 10 of 12 workflows carry
`db_access` in `step_config.json` today. Verified no `DisallowUnknownFields` on
the step-config read path, so existing values are ignored rather than rejected —
a parse failure here is exactly the deadlock vector.

Tests updated rather than deleted: `TestResolveEffectiveDBAccessIsReadWriteForEveryWorkflowStep`
now asserts the *invariant the field never enforced* across every config shape
including nil.

## E4 — `MergeAgentConfigFields` completed ✅

It copied 19 of 28 fields. The nine dropped included **`LockCode`,
`KnowledgebaseAccess`, `KnowledgebaseContribution`** — all of which gate writes.

The merge path runs whenever a step already carries in-memory `AgentConfigs`
(the else branch of the full-assign in `step_config.go`). **Zero workflows put
`agent_configs` inline in `plan.json` today** (verified across all 12), so this
was latent rather than live — but a silent write-gating failure is not something
to leave armed.

All nine added, plus `TestMergeAgentConfigFieldsCoversEveryField`: a
reflection-based guard that fails when any new `AgentConfigs` field is added
without a merge case. Deliberately reflective — the failure mode is *forgetting*,
so a hand-maintained field list would rot exactly the way the merge did.

## E5 — documentation drift ✅

- `lock_learnings_reason` (added and enforced in PLAT-059) was undocumented, and
  `step-config.md` still directed people to justify locks in `review_notes` —
  contradicting the enforced field.
- `knowledgebase_access` was still described as opt-in `none` in the tool-schema
  description, pre-PLAT-055/K, and still referenced `knowledgebase_write_method`,
  which no longer exists on the struct.

## E2 — five ORPHAN fields resolved ✅

Real runtime effect, unreachable through `update_step_config` — the same
"readable but not changeable" shape that produced the 1.0.21 deadlock. Usage
across all 12 workflows decided most of them.

**Deleted (0 usage each):**

| field | why it went |
|---|---|
| `disable_tier_optimization` | A second, un-settable and **un-reasoned** path to the same outcome as pinning `execution_tier` — which PLAT-060 had just made a decision that must state its justification. It was a loophole around that guard. |
| `todo_task_orchestrator_tier` | An `int` where every other tier is a string enum, so it bypassed tier validation and PLAT-060's reason requirement entirely. Use `execution_llm` to override a todo-task orchestrator. |
| `enable_context_offloading` | Never settable, never used. The **agent-level** `EnableContextOffloading` on `agents.AgentConfig` is untouched and still set directly by the orchestrator — only the per-step override is gone. |

**Also deleted — `learn_code_max_fix_iterations`.** Initially kept because
hetznerssh had five steps set to `0` and the default is `3`, so removing it
looked like a live behaviour change. Reading the migration showed the opposite:
`retries := 0` is the migration's own default, raised only when a legacy
message-sequence item declared `repair_with_llm`. Those five `0`s were therefore
**artifacts of an absent legacy field, not a decision** — they silently disabled
script repair for steps nobody had judged. There was never reasoning behind the
value, so there is nothing to preserve. The migration no longer derives it,
scripted steps take the uniform default, and `lock_code` remains the deliberate
way to skip the fix loop.

**Kept — `execution_max_turns`, and the ORPHAN classification was wrong.** The
audit flagged it because there is no *per-step* setter, but the struct field is
the **carrier** for the workflow-wide value: `update_workflow_config` sets
`execution_defaults.execution_max_turns`, and `readExecutionDefaults`
(`step_config.go`) delivers it to steps through this field. Deleting it would
have broken a documented, settable feature. The real defect is narrower: the
workflow-level tool description promises *"prefer per-step tuning via
update_step_config"* and no such setter exists. Left open.

## E3 — phantom clear-fields resolved ✅

`case "transport":` had an empty body returning **success while doing nothing**;
`learning_mode`, `knowledgebase_write_method` and `learnings_write_method` were
accepted as clearable with no struct field, and the answer differed depending on
whether `AgentConfigs` was nil.

They could not become hard errors: **three workflows still carry these keys**
(`learnings_write_method` ×2, `knowledgebase_write_method` ×1) and guidance
referenced some of them, so a caller following stale instructions would fail its
entire update call — nothing else in that call is applied. They also could not
stay silently successful, which is the same failure shape as `db_access`.

They are now **acknowledged no-ops**: `retiredStepConfigClearFields` maps each
retired name to why it went, the call succeeds, and the caller gets a warning
saying the field is retired and its stored value is already ignored. A genuinely
unknown name still errors. `TestRetiredClearFieldsAreAcknowledgedNotSilentlySucceeded`
pins all three properties.

## Explicitly not "cleaned up"

`declared_execution_mode_reason` has no Go consumer **by design** — its own
comment says so. It is a reviewer-facing audit trail, and PLAT-060 makes it
required. Removing it on a naive "no consumer" rule would have deleted the
mechanism PLAT-060 is built on. Likewise `successful_runs` has no setter on
purpose: it is system-managed and agents must not set it.
