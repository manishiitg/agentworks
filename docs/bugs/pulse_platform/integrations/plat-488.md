[← Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-488 — `TestGetRelayCommandCatalogWithoutGenericRuntimeRegistration` fails on origin/main

| Coordination | Value |
|---|---|
| State | open (found 2026-10-05; not fixed) |
| Priority | P3 |
| Owner | integrations |

`go test ./cmd/server -run TestGetRelayCommandCatalogWithoutGenericRuntimeRegistration` fails (`agent_profile_routes_test.go:581: wrong catalog`: the Relay command catalog returns `design-graph` first with its prompt) on a clean checkout of origin/main, unrelated to the MCP consent work that found it. Whoever owns the Relay command catalog should reconcile the expected catalog with the current commands.
