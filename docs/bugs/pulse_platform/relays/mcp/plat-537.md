[← relays / mcp](index.md)

# PLAT-537 — `TestGetRelayCommandCatalogWithoutGenericRuntimeRegistration` fails on main

| Field | Value |
|---|---|
| State | open |
| Priority | P3 |
| Product | relays |
| Area | mcp |
| Summary | open: pre-existing, found while testing PLAT-535/536. |

| Coordination | Value |
|---|---|
| State | open: found 2026-10-05 while testing PLAT-535/536; not caused by them |
| Priority | P3 |
| Date | 2026-10-05 |
| Owner | mcp |

## Evidence

`go test ./cmd/server -run TestGetRelayCommandCatalogWithoutGenericRuntimeRegistration` fails on a clean `origin/main` worktree (0f7166e3c): `agent_profile_routes_test.go:581: wrong catalog` (the Relay catalog now lists `design-graph` with a longer prompt than the test expects).
The other function-call, webhook and Relay tests in `cmd/server` pass.

## Left

Decide whether the catalog change or the test is stale (the `design-graph` command text) and update that one.

## Register notes

[PLAT-537](plat-537.md), open: pre-existing, found while testing PLAT-535/536.
