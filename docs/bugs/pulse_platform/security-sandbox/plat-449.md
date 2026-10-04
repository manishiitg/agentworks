[← Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-449 — Editable product owner selects another user's CLI identity

| Coordination | Value |
|---|---|
| State | open; confirmed in an isolated multi-user fixture |
| Priority | P1 |
| Date | 2026-10-04 |
| Owner | security-sandbox |

## Finding

Review of AgentWorks `a04b393c9`, especially PLAT-442 commits `9901c8b94`
and `5dad1a36c`, with provider `ae8e204` and mcpagent `ffc32d7`.

`product.json` is user-editable project data, but `resolveProjectOwner` trusts
its `owner_id` ahead of the physical owner, logs disagreements and still
returns that identity. `decideTurnRunAs` uses it to select a Linux slot.
Meanwhile `resolveCrewProjectBinding` admits the caller as owner based on their
own project tree. These authorities can disagree.

## Reproduction and evidence

An isolated temporary test used `newMultiUserFixture` and the actual proxy,
project binding, CLI working-directory and run-as decision helpers:

1. User A's PUT of their Code's `product.json` passes the proxy (status 0).
2. Change only `owner_id` to B, retaining every other manifest field.
3. A still resolves the project with `OwnedByCaller=true`.
4. `decideTurnRunAs` returns B and B's `slot09`, despite A being the caller
   and the project still being under A's private tree.

The probe passed, logging `[OWNER_MISMATCH]` followed by the wrong launch
identity. No real CLI was launched and no live account/files were touched;
this confirms wrong identity selection, not a demonstrated Linux exploit.
PLAT-451 records a separate tmux fallback when that script and folder disagree.

Sources: `agent_go/cmd/server/product_owner.go:203`, `run_as.go:120`,
`crew_access.go:145`, `workspace_proxy_policy.go:104` (server-owned protections
exclude project owner metadata).

## Left

Make ownership authoritative server-controlled metadata rather than accepting
an arbitrary user-writable `owner_id` for an OS identity. An authenticated
transfer, if supported, must validate and update all authorities together.
For private Code, reject mismatches with its admitted owner before launch.
Cover both browser/proxy writes and native edits; protecting only the UI is
insufficient. Add regression coverage before deploying PLAT-442.
