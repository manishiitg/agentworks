[← dominion / deployment](index.md)

# PLAT-642: Enable admin Relay testing on Dominion and deploy source-comment graphs

| Field | Value |
|---|---|
| State | in progress |
| Priority | P2 |
| Product | dominion |
| Area | deployment |
| Summary | Move Relay integration testing to Dominion; add Relays to the installed/frontend product lists with existing admin-only enforcement, then deploy the source-comment graph. |

## What happened

The owner moved testing to trader.tectonicmarkets.com. Its deployment enabled
Dominion and admin Crew, so the Relay profile/UI would be absent after deploying.

## Fix

- The existing Dominion deploy script enables relays in the installed products
  and restored frontend configuration, with work and relays restricted to admins.
- Reuse the established native build, packaged asset checks, immutable releases,
  health checks and rollback path. Preserve existing workspace data and secrets.
- Deploy PLAT-640 source-comment graphs; no separate relay.md requirement.

## Left

Deploy and verify health, product access, graph rendering and a harmless Relay run.
