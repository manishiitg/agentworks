[← relays / integrations](index.md)

# PLAT-389 — Relay API products keep Google apps and exclude Slack/WhatsApp

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | relays |
| Area | integrations |
| Summary | fixed on main; deployment pending. |

| Coordination | Value |
|---|---|
| State | fixed on main; deployment pending |
| Date | 2026-10-03 |
| Owner | integrations |

## Decision and implementation

The user clarified that Relays are API products, with optional Google apps
(Drive, Sheets, Calendar and Gmail). Slack and WhatsApp are outside their scope.

- Reuse the existing Google connection/grant panel and platform tools. Rename
  the Relay tab to Google apps. No new connection store, executor or sandbox.
- Remove the Relay-only Slack panel and Slack Builder tools in product.yaml;
  update the product prompt and relay-builder skill. WhatsApp remains excluded.
- Reject Slack connections, Slack/WhatsApp notification channels and Slack
  webhook references in Relay manifest updates. Existing saved Slack bindings
  are cleared in the execution context; channels are explicitly excluded.
- Refuse Relay targets in full and route-scoped Slack tool authorization,
  including stale sessions. Other products retain their Slack capabilities.
- Update the Relay release guide and integration acceptance checklist.

## Verification

- Targeted Relay product/catalog, manifest, scheduled INPUT/context, bot-route
  and Slack authorization regressions passed. The retained-session test rejects
  a legacy Relay Slack route before delivery. Existing non-Relay Slack tests pass.
- All 23 frontend regressions passed across the integration layout, identity
  layout and shared Google connection panel. Frontend TypeScript compilation passed.
- Shared Google scope/grant and CLI-access regressions verify optional authorized
  app access, including Drive-backed documents and separate Calendar grants.
- Commit secret scan, Go compile/lint and frontend type-check gates passed.
  Browser stays closed per the user; no deployed app or running local backend
  is changed.

## Remaining

Deploy the normal release to Excellence. Authorized account operations need a
configured Google connection and its permitted service grants. The separate
builder shell admission fix is [PLAT-380](../security-sandbox/plat-380.md).

## Register notes

[PLAT-389](plat-389.md), fixed on main; deployment
pending. Reuses authorized Google connections and removes Relay Slack surfaces,
with manifest and runtime enforcement.
