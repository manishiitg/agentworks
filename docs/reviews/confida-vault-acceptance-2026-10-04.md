# Confida Vault acceptance — 2026-10-04

## Baseline (read-only inspection)

- Existing release: `confida-e8d5e03f-20261004074953`.
- Agent, workspace and HTTP gateway are active; agent health reports idle.
- No Vault service/configuration is installed.
- Three shared MCP connections are configured: Linear (OAuth), Notion (OAuth),
  and Langfuse (Basic authorization). Catalog entries are not installations.
- Linear has a saved encrypted credential; its upstream validity is not yet
  tested. Notion's referenced credential is missing and needs sign-in.
- Langfuse needs secure Basic authorization support before migration to Vault;
  its existing Basic value must not be placed in a Bearer credential field.
- Existing project references: Linear 8, Notion 3, Langfuse 2. One workflow also
  references Resend without a corresponding installed shared connection.

## Installation contract

Confida opts into the shared rootless installer with `VAULT_ENABLED=true` and
private port `22003`. Both backend and browser product allowlists include
`mcp-gateway`; existing products, slot settings and default product are retained.
The installer bootstraps the Platform group and uses SQLite audit storage.
Vault's listener remains private; external clients use the SSO-bound product
route `/api/vault/mcp`.

## Acceptance plan

1. Save private backups of deployment settings, users, MCP metadata and OAuth
   credentials, and record the previous release for rollback.
2. Build from main, verify the manifest and stage the Confida release before
   changing the running deployment.
3. Resolve existing MCP migration before activation: enabling governed runtime
   without corresponding private/Vault connections can invalidate old catalog
   selections. Never compensate by bypassing authorization.
4. Verify private Vault health, Platform bootstrap, SQLite persistence, product
   visibility and authenticated inventory with a limited test identity/group.
5. Exercise an allowed read and a denied argument/tool, inspect actor/connector
   attribution plus input/output in audit, then revoke and retest.
6. Verify secret metadata/use permissions with a synthetic secret, never a real
   credential in chat or test output.
7. Confirm the old workload paths and connections intended for migration still
   work; preserve exact labels/connection identities and existing project choices.

Deployment and live server acceptance have not yet been completed by this record.
