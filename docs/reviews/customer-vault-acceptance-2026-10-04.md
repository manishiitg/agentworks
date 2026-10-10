# Server C Vault acceptance — 2026-10-04

## Baseline (read-only inspection)

- Existing release: `server C-e8d5e03f-20261004074953`.
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

server C opts into the shared rootless installer with `VAULT_ENABLED=true` and
private port `22003`. Both backend and browser product allowlists include
`mcp-gateway`; existing products, slot settings and default product are retained.
The installer bootstraps the Platform group and uses SQLite audit storage.
Vault's listener remains private; external clients use the SSO-bound product
route `/api/vault/mcp`.

## Acceptance plan

1. Save private backups of deployment settings, users, MCP metadata and OAuth
   credentials, and record the previous release for rollback.
2. Build from main, verify the manifest and stage the server C release before
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

Initial activation and connection migration are complete. Final live acceptance
is in progress; Notion remains pending sign-in. The legacy workflow secret
selections described below have been reconciled during deployment.

## Migration preparation

Configuration, OAuth files and user metadata are backed up privately under
`/srv/server C/state/manual-backups/vault-migration-20261004T110820Z`.
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

## White label navigation

The shared left navigation uses the deployment's same-origin `markUrl` (server C's
`/brand/icon.svg`), sized to fit the 48px rail. Horizontal headers retain
`logoUrl`/`logoDarkUrl`. Deployments without a mark use a contained wordmark;
deployments without branding add no logo. Product icons remain unchanged.
The deployment's app name, favicon and optional brand colour also apply when
switching into Vault, rather than being replaced by Vault's default branding.
The navigation still respects deployment and user product allowlists, and its
fixed/auto-hide preference remains shared across products.

## Checks before activation

- The full gateway suite passes for tool fingerprint approval, regex/full-string
  enforcement, live group grants/revocation, OAuth isolation, Basic transport,
  secret metadata/grants and SQLite persistence/audit.
- Agent runtime checks cover the shared MCP executor/bridge, private credentials,
  Vault builder authority, encrypted secret promotion/rotation/reload, explicit
  project selection, project-secret precedence, cross-user denial, revocation,
  failure of the permission service and exclusion of values from durable turns.
- Broader testing found that the workflow manifest filter hid Vault's declared
  `call_mcp_tool` during registration. Vault's product profile now admits that
  bridge explicitly; normal products, read-only turns and disabled profile tools
  receive no additional admission. Execution-time authority remains unchanged.
- server C has no `GLOBAL_SECRET_*` settings or managed global secret file.
  Eight project secret files exist in the shared project store. Their contents
  are not printed or promoted into Platform. These files and the empty user
  secret metadata file are backed up in `project-secrets.tar.gz` (0600).
- An isolated Linux instance of the release binary passed Basic authentication,
  credential redaction, allowed/denied full-string regex, denial before upstream
  execution, secret cross-user isolation/revocation, MCP revocation and SQLite
  persistence across process restart. It used only synthetic identities,
  credentials and a loopback mock upstream. Record:
  `/srv/server C/state/manual-backups/vault-predeploy-z2_sbwa_/result.json`.
- Reviewing issue #226 exposed a live-report bridge gap: report sessions were
  absent from Vault's session-owner lookup. The bridge now uses the registered
  report viewer only while the script runs, never a retained event-store owner.
  Legacy provider/tool URLs resolve uniquely to live permitted Vault tools;
  project tool selection still applies. Regression tests cover viewer identity,
  legacy mapping, selected-tool denial, revocation and ended-session rejection.
- Issue #266 identifies a separate chat admission regression: the initial
  selected-secret check ran before loading encrypted project secrets. That check
  now resolves the same execution workspace as the subsequent manifest loader,
  including preset-first phase execution and folder-first headless execution.
  Regression tests cover both priorities, preset fallback, project precedence
  over an ungranted shared secret, and refusal of ungranted shared-only secrets.
  Missing-secret errors name the missing secret; denied grants have a separate
  message. The live-report fix in PR #267 did not fix this separate bug.

## Initial live deployment

- Standard deployment activated `server C-3880f34e-20261004135433` from shared build
  `3880f34e-20261004114819`. All four product services are active, private/public
  agent health returns 200, and Vault bootstrapped Platform.
- The migration resealed Linear's existing OAuth credential for its Vault
  connection and retained Langfuse's Basic authorization in encrypted storage.
  Platform has 68 Linear tools and 86 Langfuse tools; authenticated runtime
  inventory confirms both. Thirteen existing platform identities were bound.
  Existing project selections, user roles/slots and source credentials remain.
- Notion has a connection record but no tools or runtime grant yet: its old
  credential file was missing. Complete sign-in, then sync/approve/grant its
  discovered tools through the same migration command.
- Browser inspection found Vault hidden despite its product allowlist because
  server C's runtime configuration omitted `gatewayUrl`. The configuration now
  supplies the public product origin; deployment preflight and regression tests
  check that URL. The private Vault listener is not exposed directly.
- The standard release pruning removed the prior release directory. The private
  configuration/credential and project-secret backups remain; reverting to the
  old code requires rebuilding its recorded source revisions and using the
  standard deployer rather than pointing `current` at that removed directory.

## Current live checks and outstanding workflow configuration

- Standard deployment activated `server C-f1405622-20261004141516`, built from
  `f14056220d9dcb4e86e6e43c377a92d0181bf513`. All four services and agent/Vault
  health checks passed. Browser inspection confirmed Vault is visible and opens
  with server C's branding.
- Normal runtime calls to Linear `list_teams` and Langfuse `getMetricsSchema`
  succeeded. Audit records contain the actual actor, connector, input and output.
  Both existing owners of `Workflow/testingv3` also completed an allowed Linear
  read using their own Vault runtime identity. Whole workflows were not run.
- `testingv3` selects Linear and Notion, but its tool selections contain only
  `Linear:*`. Notion needs sign-in and an explicit tool selection before that
  workflow can use it. `Workflow/customer-login` selects Resend, which was not
  installed in the pre-migration shared inventory. Its extra Linear/Langfuse
  tool entries alone do not select those servers. Seven other inspected workflow
  manifests do not select external MCP servers.
- A subsequent `Secret "ADMIN_USER" does not exist` admission error is a
  separate configuration issue. `Workflow/customer-login` has 13 selected project
  secrets; all 13 decrypt successfully with the live deployment key and the
  workflow path as authenticated additional data. Its global-secret selection
  includes those same 13 names plus `ADMIN_USER`, `LOGIN_PASSWORD`, and
  `MEMBER_USER`. None of the last three is stored in that workflow, and this
  deployment has no global secrets. `ADMIN_USER` exists in a separate Crew
  project; that does not make it available to this workflow.
- The earlier admission-loading fix does not synthesize missing credentials or
  import another project's secrets. Previously unresolved names could silently
  produce empty runtime values; current admission rejects them. Reconcile these
  selections explicitly: remove obsolete names, or securely store the intended
  values in the correct project/Vault and assign the necessary group access.
  Keep the 13 valid project secrets and do not broaden Vault grants as a workaround.
  No live secret values or selections were changed during diagnosis. The
  subsequent [one-time migration](../vault-secret-selection-migration.md)
  reconciles confirmed absent selections during deployment, with private
  backups, while retaining all existing records and group permission checks.

## Secret selection migration applied

- The standard deployer activated `server C-fb32b8bb-20261004152351` from build
  `fb32b8bb-20261004131821` (source `fb32b8bba4e20d91d0de1f12c1bd157bf48acbcc`).
  Deployment and private/public health checks passed.
- The migration scanned 36 canonical manifests. It detached `ADMIN_USER`,
  `LOGIN_PASSWORD`, and `MEMBER_USER` from `customer-login`'s global selection,
  and the absent `PAT` from `testingv2`'s project/global selections. No other
  manifest needed a change. Stored names, ciphertext and Vault grants were
  not modified by this migration.
- Original manifests and the name-only report are in the private directory
  `/srv/server C/state/migrations/secret-selections-v1/backups-2101975034/`.
  Completion is recorded in `secret-selections-v1/completed.json`; subsequent
  deployments/startups skip the cleanup.
- All nine secret-store files match their pre-deployment byte hashes. All
  13 selected `customer-login` project secrets still decrypt with the deployment
  key and their project-bound additional data. No missing global selections
  remain in that manifest. Vault's normal runtime MCP inventory is available.
  The private acceptance record is
  `/srv/server C/state/vault/secret-selection-acceptance.json`.
- The server C browser was refreshed and its unsent chat draft was preserved.
  Stored project secrets are visible with values masked. The existing manual
  workflow contract update banner (`v1.0.44` to `v1.0.45`) remains a separate
  prerequisite for starting this workflow from chat. It was not applied during
  this secret migration, and the whole workflow was not executed.
