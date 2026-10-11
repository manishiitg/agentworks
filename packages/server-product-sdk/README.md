# Server product SDK (experimental v1)

This public module defines the host boundary for separately compiled private
server products. It contains contracts, not private provider implementations.
The public gateway composition root returns no factories and builds without
private repository access.

The platform supplies authenticated identity and current administrator role.
The gateway host supplies encrypted extension storage, durable audit admission
and retained audit reads. Factories validate `APIVersion` and required capabilities
before routes mount. The optional product owns its API, provider logic and assets.

The initial v1 contracts include LLM-specific permission and cost types. They are
public data contracts; provider routing, pricing calculation, interruption logic
and product UI live in the private implementation.

Runtime private service routes accept a dedicated per-product service credential;
client actor/admin headers alone cannot establish identity. Browser/CLI traffic
must go through the authenticated platform proxy. Never publish the host's service
credential in a client configuration.

Ordinary local installations remain Goals/Crew. Optional server products require
explicit installation enablement and a composed server binary.

## Trust and installation

Product code and embedded UI are reviewed first-party code. The UI runs on the
platform origin with the person's existing session/API authority and DOM access.
The iframe and response CSP sandbox restrict browser capabilities; they do not
provide account isolation from malicious same-origin code. Never install
unreviewed assets under this boundary. See the PLAT-869 entry in
[Decisions](../../docs/DECISIONS.md).

Configure the same product-specific secret in the platform proxy and composed
host, independently of the Vault admin secret:

```sh
AGENTWORKS_PRODUCT_LLM_GATEWAY_SERVICE_TOKEN_FILE=/protected/llm-gateway-token
```

The file should contain a random secret of at least 32 characters and be readable
only by the services. `_FILE` takes precedence over the equivalent environment
variable without `_FILE`. Missing/unreadable credentials fail closed; there is no
Vault admin fallback. Each installed product must have its own credential. The
host rejects reuse of its Vault admin token. Local source launchers create a
protected separate secret when a composed gateway binary is configured.

The trusted platform hop can assert any actor/admin role within the named
product. Protect its credential accordingly. Client traffic must first pass
current account, product entitlement and role checks at the platform. User tokens
with `llm:use` can only discover models and invoke inference; management and UI
require the browser session. Per-user model permissions still apply.

Audit readers return host-stamped `Kind` (`llm` or `mcp`). Logical event IDs are
opaque and preserved independently of host storage namespaces. Admission and
completion are coalesced before filters/limits; summaries cover all matching
retained events, independent of display limits.

This first SDK includes LLM-specific types. Its general extension contract is
experimental until a second real product validates composition, credentials,
storage, audit and UI integration.

The LLM product can be the only enabled server surface. The shared host URL and
internal directory credential configure infrastructure; they do not require or
grant access to Vault's surface. Inference always uses the independent product
credential. Browser audit clients are `platform`; user-token clients are
`access-token:<token ID>`, allowing per-token audit filtering without storing the
secret. Group lookups preserve built-in/ownership metadata and detach owners.
