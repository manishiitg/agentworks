[← dominion / deployment](index.md)

# PLAT-646: Reconcile Dominion product allowlist and enable its Vault service

| Field | Value |
|---|---|
| State | deployed |
| Priority | P2 |
| Product | dominion |
| Area | deployment |
| Summary | The native deployment left a narrow AGENT_PRODUCTS in .env, which overrides the shared product profile. Vault was still disabled and uninstalled for Dominion. |

## What happened

The owner requested all products and the one-time Vault setup. The live environment
still had dominion,work,relays despite the frontend and new deploy profile exposing
all products. An application agent correctly could not edit the private host .env.
The current release already contains the manifest-verified Vault binary/installer.

## Fix

- Manage AGENT_PRODUCTS in Dominion's EXTRA_ENV so the shared deployer atomically
  replaces the stale .env entry and verifies the actual process environment.
- Enable VAULT_ENABLED using the shared private Vault lifecycle on port 21003.
  Keep its credential state outside docs, 0700 directory/0600 token, platform auth
  and SQLite audit storage. Reuse the existing authenticated agent proxy.
- Apply the host setup through operator SSH, preserve secrets and back up .env,
  bootstrap as the dominion account, then restart and verify health/product UI.
- Extend the existing deploy-profile regression to include Dominion and assert
  its Vault enablement and persistent product allowlist.

## Verification

- The focused deploy-profile regression passed; shell syntax and generated ticket
  checks passed. Dominion now includes the exact requested allowlist in EXTRA_ENV
  and enables the shared Vault lifecycle for subsequent releases.
- Operator SSH applied the one-time setup on the existing manifest-verified release
  dominion-c4034de3-20261007074844. Private .env backup saved under
  state/operator-backups; every setting except AGENT_PRODUCTS was preserved.
- Shared Vault bootstrap created the built-in Platform group. The one-time secret
  selection migration scanned four manifests and made zero changes.
- dominion-vault, agent, workspace and gateway are active. Vault /healthz and agent
  /api/health passed. The agent's real process environment contains the full
  requested product list and CAPLAYER_SERVICE_URL=http://127.0.0.1:21003.
- Vault state is owned by dominion outside docs; directory 0700, token 0600. The
  Vault application runs as dominion, not root, on loopback only.
- Authenticated in-app browser showed Goals, Relays, Dominion, Crew, Code, Vault
  and Brain; Vault loaded Access groups/Platform and the sqlite async Audit page.
  Screenshot: /tmp/dominion-products-vault-20261007.jpg.

## Left

None for the requested setup. Subsequent shared deployments retain these settings.
