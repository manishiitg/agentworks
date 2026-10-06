[← goals / plans-contracts](index.md)

# PLAT-051 — workflow upgrade instructed an agent to call an internal helper

| Field | Value |
|---|---|
| State | in progress |
| Priority | P0 |
| Product | goals |
| Area | plans-contracts |
| Summary | Workflow-contract upgrade guidance and Workshop tool surface |

| Coordination | Value |
|---|---|
| Assigned agent | `Codex` |
| Ticket state | `implemented; runtime reverify` |
| Last synchronized | `2026-08-07` |

- **Priority:** P0
- **Owner:** Workflow-contract upgrade guidance and Workshop tool surface
- **Source workflow:** Upwork scheduled run
- **Related tickets:** PLAT-049 (workflow artifact purity)

## Problem and evidence

The v1.0.21 upgrade prompt told the main agent to call
`write_workflow_manifest`. That name is an internal server helper, not a
registered agent tool. The agent correctly looked it up, received
`tools_unavailable: unknown=[write_workflow_manifest]`, and could not stamp
the required contract version. The scheduler then correctly blocked the normal
scheduled workflow rather than run after a partially completed mandatory
migration.

The durable Upwork schedule record captured the consequence:

```text
workflow upgrade preflight upgrade-1.0.21 did not stamp required version
"1.0.21" (found "1.0.20"); normal schedule message was not started
```

This is a regression of the agent-facing contract: the guidance named a
capability the registered tool index could never supply.

## Fix

- Added the narrow `set_workflow_contract_version(version)` Workshop tool.
  It validates the version and changes only `workflow.json.version` and
  `updated_at`.
- Rewrote the v1.0.21 upgrade instruction to call that real tool only after
  the migration and verification succeed.
- Added the tool to the Workshop variable/config tool set so it is present in
  the normal scheduled main-agent session.

## Verification

Focused compilation and guidance tests pass:

```text
go test ./pkg/orchestrator/agents/workflow/step_based_workflow -run '^$' -count=1
go test ./cmd/server -run TestPostRunMonitorPrependsArtifactPurityUpgradeForVersion120Manifest -count=1
go test ./cmd/server -run 'TestToolset|TestWorkshop' -count=1
git diff --check
```

## Runtime acceptance / regression check

After the backend restart, run one v1.0.20 workflow schedule that needs the
upgrade. It must expose `set_workflow_contract_version` in its tool index,
stamp `workflow.json` to `1.0.21` only after the migration succeeds, and start
the normal scheduled workflow. A missing, failed, or prematurely invoked stamp
reopens this ticket.

## Follow-up 2026-08-12 — the tool this added was unbounded in time

`set_workflow_contract_version` fixed the missing capability, but nothing
constrained *when* it could be called. A confida-login session stamped `1.0.21`
ten minutes after the scheduler had adjudicated that upgrade turn as failed,
and the next preflight trusted the value and skipped the migration. See
[PLAT-096](plat-096.md), which fences the stamp to its own open turn.
