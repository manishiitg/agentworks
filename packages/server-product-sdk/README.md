# Server product SDK (v1)

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

Runtime private service routes accept the trusted platform service credential;
client actor/admin headers alone cannot establish identity. Browser/CLI traffic
must go through the authenticated platform proxy. Never publish the host's service
credential in a client configuration.

Ordinary local installations remain Goals/Crew. Optional server products require
explicit installation enablement and a composed server binary.
