[← platform / cost-telemetry](index.md)

# PLAT-009 — `get_cost_summary` loses grouped and historical run spend

| Field | Value |
|---|---|
| State | open |
| Priority | P1 |
| Product | platform |
| Area | cost-telemetry |
| Summary | `HARNESS-COSTSUMMARY-RUNFOLDER-2026-08-03` |

| Coordination | Value |
|---|---|
| Assigned agent | `Codex` |
| Ticket state | `runtime_reverify` |
| Last synchronized | `2026-08-04` |

> Claim this ticket in this file before implementation. During active work,
> update this fragment rather than the shared index; synchronize the index
> once at handoff, review, or completion.


- **Priority:** P1
- **Owner:** cost-query projection
- **Source finding:** `HARNESS-COSTSUMMARY-RUNFOLDER-2026-08-03`
- **Source database:** `Workflow/build-in-public/db/db.sqlite`
- **Linked Social Media finding:** `HARNESS-EXEC-COST-RUN-FOLDER`
  (observed before the current implementation landed)
- **Problem:** grouped run folders are looked up through a legacy
  `runs/.../token_usage.json` path even when the authoritative execution ledger
  has the data, and an ungrouped query omits spend recorded on another date.
- **Impact:** the tool reports missing evidence or only part of the true run
  cost.
- **Current workaround:** read and sum `costs/execution/<group>/<date>.json`.
- **Implementation (2026-08-04 repair):** exact grouped queries still resolve
  one group, but an iteration-only query now enumerates every group directory
  under the authoritative scope and merges only run keys equal to that
  iteration or beneath it. Current and historical dates use the same path; the
  retired `runs/.../token_usage.json` lookup remains migration input only.
- **Verification:** focused storage tests prove an `iteration-0` query merges
  dev and production shards while excluding `iteration-9`; the next RTS Pulse
  LLM/Ops review must reverify the real tool result.
- **Regression test:**
  `TestReadRunAcrossDatesResolvesIterationAcrossGroupedShards`; the complete
  `agent_go/pkg/orchestrator` package passes on 2026-08-04.
- **Acceptance:** query results reconcile to authoritative cost events across
  dates, groups, models, and execution IDs without double-counting aggregate
  and per-step views.

## Execution-keyed compatibility update — 2026-08-06 (Codex)

`get_cost_summary` reaches `GetRunTokenUsageFile` and its date-shard reader.
That reader now consumes the new `executions[execution_id]` ledger records,
matches against their effective (archived-or-current) path, and falls back to a
legacy `run_folders` entry only when the requested run has no v2 record in that
shard. This prevents a migration-day v1 projection from being added on top of
the same UUID cost record. The focused test is
`TestReadRunAcrossDatesUsesExecutionKeyedRecordsNotLegacyProjection`.

Runtime reverify remains: run a workflow through an `iteration-0` rotation and
compare `get_cost_summary` against the UUID-keyed JSON records.
