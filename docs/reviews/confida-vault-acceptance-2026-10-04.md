# Confida Vault acceptance — 2026-10-04

## Baseline (read-only inspection)

- Existing release: `confida-e8d5e03f-20261004074953`.
- Agent, workspace and HTTP gateway are active; agent health reports idle.
- No Vault service/configuration is installed.
- Three shared MCP connections are configured: Linear (OAuth), Notion (OAuth),
  and Langfuse (Basic authorization). Catalog entries are not installations.
- Linear has a saved encrypted credential; its upstream validity is not yet
  tested. Notion's referenced credential is missing and needs sign-in.
- Langfuse requires Basic authorization. Custom Vault connectors now support
  an explicit `AuthScheme=Basic`; the existing encoded credential is stored
  encrypted, remains absent from inventory, and rotation preserves its scheme.
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

## Migration preparation

Configuration, OAuth files and user metadata are backed up privately under
`/srv/confida/state/manual-backups/vault-migration-20261004T110820Z`.
The rollback record points at the previous release above. This backup does not
copy project content; the MCP migration does not modify it.

The operator-only command `server import-vault-oauth --connection-id ID
--mcp-config FILE [--apply]` reads the deployment's existing OAuth entry and
reseals its credential/configuration for the matching live Vault connector.
Run as the service account with its `AUTH_SECRET`, HOME and Vault service
settings. The default is read-only. Missing tokens report sign-in required;
existing destinations are not overwritten, including after token refresh.
Old credentials are retained for rollback. Provider endpoints and connection
identity must match the live Vault record. Credential contents are never printed.

For each migrated connection, approve the discovered fingerprints and grant
the existing tool set through Platform, preserving the previous global sharing.
Keep the original labels so unambiguous existing project selections resolve;
verify the runtime mapping before removing any legacy entry. New tools require
explicit approval/grants. Notion still needs the administrator's browser sign-in.
