[← Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-487 — Connecting an MCP client answered "404" on Allow where Builder/Relay authoring is switched off, and the consent page listed everything

| Coordination | Value |
|---|---|
| State | fixed on main (consent scopes, refusal reason, simpler page); deployed: see the Update line below |
| Date | 2026-10-05 |
| Owner | integrations |

## What happened

Connecting Claude Code to Excellence (`/api/external/v1/mcp`, OAuth) asked for every advertised scope (`mcpOAuthScopes`, including `builder:chat` and `relays:write`). The consent page listed ten permissions and a "workflows this connection may build" box (the only choice was the owner's `test` workflow). Allow answered `404` (`POST /api/oauth/mcp/consent`, 20:44:06 on 2026-10-04): `Decide` validated the REQUESTED scopes, `relays:write` needs `AGENTWORKS_MCP_BUILDER_ENABLED=true`, which is unset in the running agent on Excellence (and RTS), and every `Decide` error was mapped to a bare 404 that the page showed as "Request failed with status code 404". The Relays product being on for a user is a different switch (`userAllowedProduct`); the external Builder/Relay authoring is gated by that env flag.

## Fix

- `mcpOAuthScopesFor` drops `builder:chat` where the server flag is off and `relays:write` unless the flag is on AND the account may edit and has the Relays product (the same rule the consent check enforces; on Excellence only 2 of 10 users have Relays); the consent GET, the consent POST and `Decide` all use the scopes that will really be granted, so a client that requests everything is approved for the rest.
- A refusal the person can act on is returned as `400` with `error_description` and shown on the page instead of the 404 (`mcpOAuthRefusal`).
- The page leads with up to five plain lines (see and run workflows, use Crews, make changes, review Code, Vault tools); the exact permissions are behind "Show details".
- Tests: `mcp_oauth_scopes_test.go`; `MCPOAuthConsent.test.tsx` (grouping, refusal reason); `builderOAuthSetup` now switches Builder on explicitly.

## Not done

A client still cannot ask for a smaller set of scopes through Claude Code's `mcp add`; it requests all advertised ones. Whether Excellence should enable `AGENTWORKS_MCP_BUILDER_ENABLED` is the owner's decision (it lets an MCP client edit plans/code and Relays).
