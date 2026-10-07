[← dominion / deployment](index.md)

# PLAT-642: Enable admin Relay testing on Dominion and deploy source-comment graphs

| Field | Value |
|---|---|
| State | deployed |
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

## Verification

- Local deploy script syntax and ticket checks passed. Main commit 29717feae.
- `./deploy.sh dominion --activate` passed native binary, production frontend,
  packaged asset, preflight, running configuration and health checks.
- Active release: `29717fea-20261007072134`. Agent, workspace and gateway active;
  workspace health confirms Landlock available. Installed products are
  dominion,work,relays; admin-only products are work,relays. Frontend list includes
  relays. Existing server-side filtering preserves regular-account restrictions.
- In-app browser: created **Relay graph smoke test**, saw the seeded graph and
  selected node details; Builder tested INPUT {"name":"Ada"} via test_relay and
  get_relay_run. It created the required function trigger, completed run
  c8ddcc1a-009f-5eab-9f9b-560176734de3 and returned {"hello":"Ada"}.
- Runs showed completed status and persisted JSON; run graph rendered. No source
  changes, publication, agent calls or other workflow execution in this test.
- Screenshot: /tmp/relay-dominion-graph-20261007.jpg. The test Relay remains available
  for the owner to inspect. Existing production workflow data was preserved.

## Left

None for the requested Dominion deployment. No other deployment was changed.
