[← platform / integrations](index.md)

# PLAT-474 — OAuth consent screens say "Multi Agent Builder", not AgentWorks

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | integrations |
| Area | integrations |
| Summary | fixed on `main` for newly authorized servers, needs a restart: dynamic client registration now names the app AgentWorks. |

| Coordination | Value |
|---|---|
| State | fixed on `main` for servers authorized after it; needs a rebuild and restart |
| Date | 2026-10-04 |
| Owner | integrations |

## Source

When the owner authorized an MCP server by OAuth, the consent screen named the app
"Multi Agent Builder" (plus a "default" the owner saw next to it).

## Cause

Dynamic Client Registration (`mcpagent/oauth/discovery.go`) sent
`client_name: "Multi Agent Builder"` and a placeholder `client_uri`
(`github.com/your-org/...`). The remote server shows that name. The registration is
cached per user and server (`<tokens>/<user>/<server>.client.json`), so servers
authorized earlier keep the old name.

## Done

- mcpagent registers as `AgentWorks` with `https://agentworkshq.com`
  (`oauth.ClientDisplayName`, `oauth.ClientHomepage`); a test checks the request body.
- Builder pins mcpagent `70d8d89`.

## Left

- Servers already authorized keep "Multi Agent Builder" until their cached registration
  is removed and the server is authorized again; the owner chose not to migrate them.
- The word "default" next to the name is not in our registration payload. It is most
  likely the remote service's own label (workspace or account); not traced.

## Register notes

[PLAT-474](plat-474.md), fixed on `main` for newly authorized
servers, needs a restart: dynamic client registration now names the app AgentWorks.
