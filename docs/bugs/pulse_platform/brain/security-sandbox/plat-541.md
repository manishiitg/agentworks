# PLAT-541: Vault and Brain are core products, on in every installation

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | brain |
| Area | security-sandbox |
| Summary | on main, not deployed: `AGENT_PRODUCTS` no longer switches off Vault or Brain; accounts and roles decide access; every active account may use Brain through the MCP, the app tab needs the `knowledgebase` product. |

**State:** on main, not deployed. P2.

**Decision (owner, 2026-10-05):** Vault (`mcp-gateway`) and Brain (`knowledgebase`) are always on, like Crew and Code, in every installation including local. Only accounts and roles change who can do what.

**Done:**
- `productEnabled` returns true for both whatever `AGENT_PRODUCTS` lists (`cmd/server/server.go`, `coreProducts`). The default frontend surfaces now include `mcp-gateway`.
- Every active account may connect to Brain through the MCP and local tokens (`knowledgebaseMCPAllowed`); the app tab needs the `knowledgebase` product on the account (administrators have it). Agents running as a person may use Brain tools within their folder roles and a bound folder, without the app product (`knowledgebaseExecute`).
- Content is never opened by this: administrators are Owner of every folder; everyone else sees nothing until a folder grant exists. There is no all-members grant (owner decision).
- RTS, Confida and the Excellence product list `knowledgebase` in their service and frontend lists (RTS deploy guards updated).
- One test pins the MCP/app split; the product-enabled test and two older tests were updated to the core rule.

**Exempt (owner, 2026-10-05):** SparkQuill and Dominion are their own products: a deployment whose `AGENT_PRODUCTS` lists `sparkquill` or `dominion` keeps its allowlist and does not get Vault or Brain.

**Left:** deploy RTS and verify live (consent screen for a non-admin, the tab for an account with the product, an agent run with a bound folder).

## Register notes

[PLAT-541](plat-541.md), P2, on main, not deployed: `AGENT_PRODUCTS` no longer switches off Vault or Brain; accounts and roles decide access; every active account may use Brain through the MCP, the app tab needs the `knowledgebase` product.
