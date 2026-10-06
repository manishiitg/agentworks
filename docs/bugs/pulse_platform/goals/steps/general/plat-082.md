[← goals / steps / general](index.md)

# PLAT-082 — Cluster K: failed async children were reported completed; two findings were already-fixed or incorrect

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | goals |
| Area | steps/general |
| Summary | P1 for the dispatch-status defect |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | `implemented` — runtime reverify pending for the one real defect |
| Last synchronized | `2026-08-10` |

- **Priority:** P1 for the dispatch-status defect
- **Owner:** todo-task sub-agent execution boundary
- **Source findings:** `ff9832ccb79e1086` (build-in-public),
  `0b7d9d4a1b498e71` and `5e248d9ec6e7244f` (social-media)

## Finding 1 — `ff9832ccb79e1086`: already fixed

The stored finding says provider-profile role `execution_low` resolves to
Claude Haiku 4.5 at `high` effort. That was true when filed, but the current
provider-owned default was already changed in `multi-llm-provider-go`
`233f3c6`: Claude `Low` is now
`claude-haiku-4-5-20251001` with `reasoning_effort=medium`.
`TestCodingAgentDefaultTierModelsClaudeExecutionTiers` pins the corrected
mapping. No additional platform patch belongs in this ticket; reverify
`get_llm_config` after the consuming server uses that provider commit, then
close this fingerprint.

## Finding 2 — `0b7d9d4a1b498e71`: real defect, fixed

### Captured evidence

The retained Social Media orchestrator conversation contains ten child
dispatch completion envelopes with `status:"completed"` even though child
initialization failed and the result began with `ERROR:`. The parent therefore
listed failed routes under `completed_steps`.

### Root cause

`createExecutePredefinedSubAgentSyncFunc` and
`createExecuteGenericAgentSyncFunc` received a real Go error from the child
executor, but converted it to an `"ERROR: ..."` result string and returned a
nil error. That conversion was originally intended to make synchronous tool
failures readable to the orchestrating agent.

The async path calls those same functions below the virtual-tool boundary.
`runAsyncCall` therefore saw `err == nil`, and `waitForUnreconciled` correctly
classified the information it had as `completed`. This is distinct from
PLAT-032's child-token telemetry issue and from PLAT-073 cluster A's HTTP
`Success` flag: the failure was erased one layer earlier, inside the workflow
orchestrator.

### Fix

The sync execution functions now preserve and return the real Go error. The
existing virtual-tool handler still converts that error into a typed,
agent-readable `SubAgentResult{success:false,error:...}` for synchronous tool
calls. The async owner retains the same error and emits
`status:"failed"`, `error:<message>`, plus any partial result evidence.

Regression tests cover both boundaries:

- an async child execution error produces one failed terminal completion;
- the synchronous virtual tool still returns a readable `success:false`
  result envelope rather than losing the evidence.

### Runtime acceptance

After restart, a deliberately safe child initialization failure must appear
as `status:"failed"` in the automatic completion batch and must not be counted
as a completed route by its parent. Only then is `0b7d9d4a1b498e71` ready to
close.

## Finding 3 — `5e248d9ec6e7244f`: not a defect

The finding inferred deletion because the new active
`runs/iteration-0/default` contained only the current run's artifacts. That is
the intended layout. Before a full run, `rotatePairedIterationZero` moves the
**entire** previous workflow run folder to the next monotonic
`runs/iteration-N`; it does the same for the paired evaluation run, then
creates a fresh `iteration-0`. `run_retention_count` retains five backups by
default and prunes only older backups.

Current Social Media evidence confirms the behavior: `iteration-250` through
`iteration-254` are retained complete run folders, while `iteration-0` is the
current run. Some backup trees are intentionally gitignored; absence from a
tracked Git diff is not deletion from the workspace.

`execution_defaults.always_use_same_run=false` does not mean every run has a
new active folder name. The runtime always uses `iteration-0` as the active
name and rotates its previous occupant first. Builder system instructions and
the run-manager comments both state this contract.

This finding should be rejected as a misread after runtime verification, not
closed as a shipped platform repair.

## Verification

- `go test ./pkg/orchestrator/agents/workflow/step_based_workflow -run TestRunAsyncCallPreservesChildExecutionFailure`
- `go test ./cmd/server/virtual-tools -run TestHandleCallSubAgentReturnsTypedFailedEnvelope`
- `go build ./...`
- Full baseline remains exactly 22 pre-existing failures.

