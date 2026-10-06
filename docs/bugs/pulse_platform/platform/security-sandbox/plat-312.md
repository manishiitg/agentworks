[← platform / security-sandbox](index.md)

# PLAT-312 — Shared gog authentication store with direct terminal access

| Field | Value |
|---|---|
| State | deployed |
| Priority | - |
| Product | platform |
| Area | security-sandbox |
| Summary | Trusted builder and execution shells use the real gog CLI directly. |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | Implemented and deployed to RTS; verification scope below |
| Last synchronized | 2026-09-12 |
| Implementation commits | 4d7725c22 |

## Delivered behavior and verification

Trusted builder and execution shells use the real gog CLI directly. Application and executor resolve GOG_HOME, then XDG_CONFIG_HOME/agentworks/gog, then the service user's default path. The configured store is granted read/write for credential lookup, locks and token refresh; normal account/client flags remain available. Restricted profiles do not inherit the grant; operators can disable automatic terminal access.

New Google connections use gog as token owner; AgentWorks retains connection metadata. Verified migration supports legacy credentials without deleting independent gws credentials. gws stays available; reconnect/migration determines the auth backend. Split deployments must mount/configure the same store in both processes.

RTS uses its service-owned GOG_HOME. The Linux sandbox regression passed when invoked with that deployed environment. The generic build preflight skips without that environment; this is not a mailbox read verification. No claim is made that every legacy connection was migrated. See [Google CLI authentication](../../../../google-cli-authentication.md). Related: [PLAT-281](plat-281.md).

## Deployment receipt

Included in RTS app release `bb7ac6d17ec43750e74a5c92d73ef067f66c69bf`
(`bb7ac6d-20260912135437`), with provider `570ede69fb85beef251ddca9792a2e5ad0dfe95d`.
All three services were active and the public health endpoint was healthy after deployment.
Deployment health is distinct from the feature-specific acceptance scope above.
