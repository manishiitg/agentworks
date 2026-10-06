[← brain / general](index.md)

# PLAT-634: Brain missing from the local product switcher

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | brain |
| Area | general |
| Summary | The local launcher's product list left out Brain, so the local switcher hid it |

## What happened

Owner, 2026-10-06: Brain did not show in the local app. The server enables Brain everywhere (`productEnabled`,
`coreProducts`), but `run_server_with_logging.sh` writes the frontend's `enabledProductSurfaces` from a fixed default
list (`agentworks, relays, work, code, mcp-gateway`) that left out `knowledgebase`, and an explicit list replaces the
frontend's own default (which includes Brain).

## Fix

The launcher's default list includes `knowledgebase`. Explicit lists elsewhere stay as configured (a dedicated Video
Studio host deliberately shows only its products). Takes effect on the next local start.
