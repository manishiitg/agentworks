# Deployment product capabilities (PLAT-783)

Prepared for desktop release v1.25.135. Deployment mode is explicit; a loopback
address, local login or SSO configuration does not determine installed products.

| Installation | Default product set |
| --- | --- |
| Source local (default) | Goals/workflows, Crew, Code, Flows/Relays, Brain, Vault, AgentBridge |
| Packaged desktop / DMG | Goals/workflows, Crew |
| Server | Configured server products and account permissions |

Packaged desktop users use their own CLI for standalone coding. Workflow/Crew builders
retain their native CLI providers and workflow script authoring.

Normal local installs retain project MCPs, project secrets, local knowledge and
workflow learnings. The packaged desktop does not include Code or the four shared server
products or support the full source-development profile.

From `agent_go`, run `./run_server_with_logging.sh` for the full local stack,
or add `--with-workspace` for the backend and workspace without launching the
frontend. Server products are always on locally, and Vault must start
successfully. An installed private LLM Gateway binary adds that product to the
default source profile; an explicit surface list still narrows availability.

Local Vault audit storage defaults to SQLite. The source launcher also replaces
the retired `VAULT_AUDIT_PROVIDER=clickhouse` setting with `sqlite`, including
when it comes from `.env`. An explicit `VAULT_AUDIT_PROVIDER=off` disables audit
collection. Other invalid provider values still fail validation. Existing local
data and audit retention settings are preserved.

## Shared policy

`agent_go/pkg/productpolicy` intersects:

1. `AGENTWORKS_DEPLOYMENT_MODE=local|server`.
2. `AGENTWORKS_LOCAL_SERVER_PRODUCTS=1`, always set by the source launcher.
3. `AGENTWORKS_ENABLED_PRODUCT_SURFACES`, when explicitly configured.
4. `AGENT_PRODUCTS`, retaining the existing shared-server Brain/Vault baseline
   unless an explicit surface selection excludes them.

An old local surface list containing only excluded server products falls back
to the normal local product set. An account can narrow this policy and cannot
enable an uninstalled product. Product availability remains separate from
project grants, token scopes, Vault group/tool/regex permissions and Brain folder
roles. Existing external Brain tokens retain their folder-authorized server
access even without a Brain app-home entitlement.

Disabling Vault also removes shared secret names/values from runtime selection;
it does not fall back to unrestricted legacy global-secret access. A project copy
with the same name still follows its existing project permissions and precedence.

## Agent and transport boundaries

- Resolved product profiles and client-visible feature metadata omit disabled
  tools and skills without modifying the canonical manifest registry.
- Agent wrappers and workflow base agents project system instructions, trusted
  builtin skills and supporting references, tool descriptions and schemas.
  Workflow steps, schedules and delegated agents use these shared constructors.
- Native bridges advertise admitted tools. Bound executors recheck current
  availability, so retaining a tool definition does not grant access.
- Attached and fallback skill reads filter product-owned skill names. Known
  disabled skill projections carrying `.agentworks-managed` are removed before
  a CLI starts. User-authored/unmarked folders and symlinks remain intact.
- External platform MCP catalogs and guidance use the installation policy;
  handler-specific authorization continues to apply.

Trusted optional text uses `<!-- product:<id> -->…<!-- /product -->` blocks.
Only explicit blocks are projected; arbitrary user wording is not removed by
keyword. Imported project skills and learnings are not rewritten. Keep new
platform tool/skill ownership declarations in `productpolicy` current when
adding product capabilities.

Previously migrated shared knowledge remains marked as shared when Brain is
disabled. The agent receives an unavailable-product explanation, and the retired
local archive stays blocked. No secret/knowledge migration is performed here.

## Verification and activation

Regression tests cover the full and minimal local profiles, local audit defaults,
shared server, explicit server allowlists, canonical Workflow/Crew/Code guidance, resumed definition
assembly, cached execution, step skill resolution, external MCP, safe skill
cleanup and existing reader/bot/project grants. Product navigation/Ctrl+K and
desktop packaging tests cover the local product set. These checks use no paid model requests.

Restart the backend to load the implementation. For a changed installation
profile, restart its native CLI conversations as well. Saved chat history remains
historical context; it is not rewritten. No deployment to hosted servers or model requests are required by this change.
