[← sandbox / secrets](index.md)

# PLAT-276 — A secret created by `set_workflow_secret` was invisible to `execute_shell_command` in the same chat turn

| Field | Value |
|---|---|
| State | deployed |
| Priority | P2 |
| Product | sandbox |
| Area | secrets |
| Summary | P2 — the agent sets a secret and uses it seconds later; the shell ran without `SECRET_<name>` until the next message. |

| Coordination | Value |
|---|---|
| Assigned agent | Claude Code |
| Ticket state | `fixed` — deployed to RTS 2026-09-03 and verified live; see Verification |
| Last synchronized | `2026-09-03` |

- **Priority:** P2 — the agent sets a secret and uses it seconds later; the shell ran without `SECRET_<name>` until the next message.
- **Owner:** `agent_go/pkg/workspace/client.go` (`SetExtraEnv`/`DeleteExtraEnv`/`extraEnvSnapshot`), `cmd/server/virtual-tools/workspace_advanced_tools.go` (session shell-client registry), `cmd/server/workflow_phase_tools.go` (secret tool hooks).
- **Origin:** live, RTS `rtslatency` Builder chat 2026-09-03 11:08 UTC: `set_workflow_secret` at 11:08:10, `execute_shell_command` at 11:08:13 in the same turn; operator asked whether the secret was set dynamically.

## What happened

The secret tool's `afterUpsert` hook updated the Workshop session's live env map and the workflow manifest (steps and scheduled runs saw the value immediately), but the chat agent's own shell client had cloned its env at turn start (`workspace.WithExtraEnv` → `cloneEnvMap`, introduced in `3169ee7df`). The executor factory's comment still promised that in-place writes to the returned map propagate; that has been false since the clone.

## Fix (agent_go `f732a0939`)

- `workspace.Client` gained a locked `SetExtraEnv` / `DeleteExtraEnv` / `ExtraEnvValue`; every `ExecuteShellCommand` reads a per-request snapshot, so concurrent updates cannot race a request being assembled. `WithExtraEnv` keeps its clone semantics.
- `CreateWorkspaceAdvancedToolExecutorsWithSessionAndEnv` registers each session's live clients (bounded to the 8 most recent; each turn creates a fresh client). `SetSessionShellEnv` / `DeleteSessionShellEnv` update them all.
- The workflow-phase secret hooks push `SECRET_<name>` into those clients on upsert and remove it on delete, in addition to the Workshop update.

## Verification

- Unit (race detector): `TestSetExtraEnvReachesNextShellRequest` (the request after `SetExtraEnv` carries the var, after `DeleteExtraEnv` it does not), `TestWithExtraEnvDoesNotAliasCallerMap`, `TestSetSessionShellEnvReachesEveryLiveClientOfTheSession`, `TestSessionShellClientsAreBounded`.
- Live: pending the next RTS turn that sets a secret and echoes it in the same turn.

