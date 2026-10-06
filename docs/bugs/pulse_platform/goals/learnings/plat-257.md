[← goals / learnings](index.md)

# PLAT-257 — Step learnings/knowledge access contract diverged across prompts, filesystem guards, and Builder guidance

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | goals |
| Area | learnings |
| Summary | runtime permission contract, severity high. |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | `implemented; runtime reverify` |
| Last synchronized | `2026-08-30` |

- **Priority:** runtime permission contract, severity high.
- **Related:** [PLAT-061](../steps/config/plat-061.md)), [PLAT-062](../steps/scripted/plat-062.md)),
  [PLAT-124](../../browser/automation/plat-124.md)), [PLAT-223](plat-223.md).
- **Origin:** user-requested audit of whether every workflow step can read the
  shared learnings and knowledgebase stores.

## Problem

Normal steps already defaulted to read access for both stores, but the contract
was inconsistent in two important ways:

1. `canReadLearnings` intentionally excludes evaluation-mode and deterministic
   routing steps from prompt injection, while regular, scripted, todo,
   message-sequence, retry, and continuation filesystem guards independently
   called `resolveLearningsAccess`. Evaluation agents could therefore receive
   shell/file read permission for learnings that their prompt deliberately
   omitted. The execution-agent factory could also re-broaden a guard after an
   outer caller narrowed it.
2. The runtime's unset `knowledgebase_access` default has been `read` since
   PLAT-055/K, but the Builder tool schema, the `AgentConfigs` source contract,
   and `UseKnowledgebase` comments still said `none`/opt-in. PLAT-061 claimed
   this documentation drift was fixed, but only the canonical reference was
   updated; the live Builder schema remained incorrect.

## Fix

- Added `resolveExecutionLearningsAccess` as the shared prompt/filesystem
  capability decision.
- Routed ordinary execution, execution-agent creation (including retries and
  recovery), scripted execution, todo orchestration, and message sequences
  through that decision.
- Evaluation message sequences now suppress only learnings writes; configured
  DB/KB behavior is preserved.
- Corrected every stale Builder/source description to state that KB reads are
  default-on and `none` is the explicit opt-out. Writes remain gated by a
  contribution contract.

Human-input and deterministic routing steps execute no agent and therefore
receive no filesystem capability. Evaluation steps keep KB reads but no
learnings access. All ordinary agentic step forms default to read access for
both stores.

## Verification

- Table-driven coverage for regular, message-sequence, todo, evaluation,
  routing, and explicit-`none` learning resolution.
- Message-sequence evaluation coverage proves learnings writes are suppressed
  without broadening or removing DB/KB writes.
- `go test ./pkg/orchestrator/agents/workflow/step_based_workflow` passes.

Runtime reverify: after deployment, inspect one ordinary step, one todo or
message-sequence step, and one evaluation run's effective Folder Guard.


## 2026-09-09 local Pulse audit follow-up

Retain explicit builder acceptance cases for generated guidance coherence:
DB guidance must name accessible managed tools/read surfaces; reusable learnings
must not contradict plan execution environment or variable names. Sales Outreach
had a `GOG_HOME` fallback forbidden by its plan plus `$GOG_HOME` versus
`$VAR_GOG_HOME` confusion; its workflow repair was subsequently verified across
three normal runs (`PUL-93DC16E9`). That repair is not proof that the builder now
prevents every equivalent contradiction. Validate knowledgebase/learnings against
the executable contract at authoring/change time, not on every healthy Pulse tick.

LinkedIn `PUL-D3AD004C` is another acceptance case: a verifier deleted a successfully
posted comment's DB receipt after relying on an earlier failed snapshot. Require
same-execution side-effect evidence before destructive reconciliation. This is
not yet reduced to a shared deterministic platform-code reproduction; do not
mark that prevention complete from the workflow-level prompt repair alone.


## 2026-09-10 improvement-system dependency

[PLAT-305](../pulse/reviews/plat-305.md)) implements the first review/scheduling/research/lifecycle
release. This ticket's remaining foundation acceptance is still required and is
not closed by adding Architecture or outcome tracking. Historical evidence and
legacy workflow behavior remain unchanged by that release.
