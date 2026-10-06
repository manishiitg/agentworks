[← goals / step-execution](index.md)

# PLAT-172 — a step can be instructed to write another step’s execution output

| Field | Value |
|---|---|
| State | open |
| Priority | P1 |
| Product | goals |
| Area | step-execution |
| Summary | P1 — one contradictory handoff instruction can make a long workflow turn fail after completing useful work, so the scheduler reports the whole occurrence as failed. |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `open` |
| Last synchronized | `2026-08-21` |

- **Priority:** P1 — one contradictory handoff instruction can make a long workflow turn fail after completing useful work, so the scheduler reports the whole occurrence as failed.
- **Owner:** workflow plan contract/guidance, execution prompt rendering, and the typed plan-review surface.
- **Related:** [PLAT-062](plat-062.md), [PLAT-162](plat-162.md), and [PLAT-169](../../app/ui/plat-169.md)).

## Incident

Social Media's `execute-verify` step was correctly limited to writing under:

```text
runs/iteration-0/default/execution/execute-verify/
```

The plan first told it to write `action_queue_refreshed.json` in its **own** declared output folder. Later the same description also instructed it to copy that result to:

```text
runs/iteration-0/default/execution/execute-intent-queue/action_queue.json
```

The latter is owned by `execute-intent-queue`. The Folder Guard correctly denied the write. The agent then ended with a terminal failure even though it had produced useful verification work; the parent Codex scheduler conversation subsequently had no valid final response to present.

This is not a request to widen the guard or manually patch the Social Media plan. The Guard is enforcing the intended isolation boundary.

## Why the boundary exists

Each execution step owns its output folder. Other steps may read declared upstream outputs, but cannot mutate them. That prevents parallel or resumed steps from silently overwriting one another's evidence, leaving an output that cannot be attributed to its real producer.

```text
producer writes own output → declared dependency/handoff → consumer reads it
```

If a new artifact is required, the producing step owns it in its folder. If an existing artifact must be revised, the owner step — or an explicitly declared handoff/aggregation step with its own output — performs the revision. An ordinary downstream verifier cannot patch a sibling's output.

## Root cause

The platform renders exact narrow write grants but lets plan descriptions contain arbitrary prose about execution paths. Plan validation checks graph and schema structure, yet does not give the reviewing agent a first-class view of ownership for every named execution path. A plan editor can therefore add a later “copy it to step B” instruction that contradicts the step's actual write grant and earlier output instruction.

The current denial does not identify the target owner, whether a read handoff already exists, or the safe repair choices. Agents can waste turns retrying or convert one clean plan inconsistency into an opaque scheduler failure.

## Proposed repair

1. **Expose ownership as plan-review evidence.** `validate_plan_change` and review guidance should return the selected step's writable output root, referenced upstream output roots, declared dependencies, and the owner of every named execution path. The agent remains responsible for reading the complete plan and deciding the correct handoff; Go does not rewrite the plan.
2. **Add an ownership contradiction gate.** When a changed description names a concrete `runs/.../execution/<step-id>/...` target outside the edited step's own output root, flag it before persistence with the target owner and require an agentic correction. This is a narrow structural guard, not a natural-language plan generator or broad mechanical reviewer.
3. **Make runtime denial actionable.** Folder Guard errors for a known sibling execution path should name the owning step and say: write in this step's output folder, declare/read a handoff, or change the owner step through typed plan tools. The error remains terminal for the current turn: no guard widening or cross-step fallback.
4. **Teach planner and reviewer the contract.** Plan/design and Pulse Review guidance must require an explicit producer → handoff → consumer path for every cross-step artifact and inspect altered descriptions for contradictory later instructions.

## Acceptance

- A plan change that names a sibling's execution folder as a write target is rejected with the real owner and an actionable repair path.
- A legitimate downstream step can read its declared upstream artifact without a sibling write grant.
- The normal Folder Guard remains narrow; no step receives broad `execution/` write access.
- The live denial includes the owning step and produces no retry loop.
- Pulse Review followed by Pulse Fixer can repair the contradiction through typed plan tools; a producing run proves the repaired handoff without manual plan editing.

## Decision history

- **2026-08-21:** Preserve per-step output ownership. The remedy is better plan evidence, actionable diagnostics, and agentic plan repair — not relaxed filesystem authorization or backend artifact moves.
