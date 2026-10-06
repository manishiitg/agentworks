[← platform / integrations](index.md)

# PLAT-509 — Free search MCPs in the MCP catalog (after `search_web_llm` was removed)

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | integrations |
| Area | integrations |
| Summary | fixed on main: Firecrawl added next to Exa and Parallel (both already there); card text says free tier. |

| Coordination | Value |
|---|---|
| State | fixed on main; needs a restart (catalog is read at startup) |
| Date | 2026-10-05 |
| Owner | integrations |
| Related | PLAT-508 (`search_web_llm` removed) |

## Source

With `search_web_llm` gone (PLAT-508), the free hosted search MCP services are available only as catalog connectors people add themselves.

## State of the shared catalog (`agent_go/configs/mcp_servers_clean.json`, 90 servers before this change)

- `Exa` (`https://mcp.exa.ai/mcp`) and `ParallelSearch` (`https://search.parallel.ai/mcp`) were already there, keyless. The frontend already mapped their card text and the `search` category.
- `Firecrawl` was missing ("Tavily and Firecrawl later" in `catalog.ts`).
- Both server deployments (`deploy/aws-ec2/server/mcp-servers.override.json`, `deploy/rootless-linux/products/confida/mcp-servers.override.json`) remove `ParallelSearch` on purpose (`"ParallelSearch": null`); left as is.

## Done

- Added `Firecrawl` (`https://mcp.firecrawl.dev/v2/mcp`) to the shared catalog, with a card description and the `search` category. The card text of Exa, Parallel and Firecrawl now says "free tier, rate limited".
- Checked: an MCP `initialize` request to all three URLs answers HTTP 200 without a key (handshake only; tool calls on the free tiers can still be rate limited, which is why the old tool kept failing).

## Left

- The gateway's embedded snapshot (`mcp-gateway/internal/catalog/catalog.json`, 62 providers; has `Exa`, not `ParallelSearch` or `Firecrawl`) was not changed.
- Server deployments get Firecrawl too (only `ParallelSearch` is removed there); add `"Firecrawl": null` to the two overrides if free keyless search should not be offered on servers.
- No place for a user's own Exa / Parallel / Firecrawl API key in these catalog entries; they are keyless only.

## Register notes

[PLAT-509](plat-509.md), fixed on main: Firecrawl added next to Exa and Parallel (both already there); card text says free tier.
