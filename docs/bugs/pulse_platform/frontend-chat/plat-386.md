[← Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-386 — Providers owns agent setup; products select ready accounts and models

| Coordination | Value |
|---|---|
| State | fixed on main; included in recorded RTS source release; app-level live qualification pending |
| Date | 2026-10-03 |
| Owner | frontend-chat; runtime forwarding in coding-agent-bridge |

## Problem and decision

Workflow, Crew, Code and Relay model panels mixed runtime selection with token,
API-key and provider setup. They also offered unavailable coding providers,
used inconsistent account labels and hid reasoning effort inside Model.
Providers is the shared place to install/configure agents and manage accounts;
products consume ready accounts and select models and supported reasoning effort.

## Done on main

- Product selectors require an installed CLI and a configured, usable account
  authorized for the current project/product. A working personal account remains
  selectable when the installation account is signed out. Saved unavailable
  selections remain visible for diagnosis; opening settings does not replace them.
- The installation account is labeled **Admin-managed account**, including new
  project creation. Providers keeps the top header Back control, removes the
  duplicate inner Back control and uses the Antigravity brand icon.
- Removed product-scoped token/API-key fields and embedded provider setup from
  `WorkflowLLMConfigurationPanel`; management links open Providers. Existing
  stored credentials remain compatible; this change does not migrate them.
- Crew/Code effort choices stay visible when Model is collapsed and intersect
  profile choices with selected-model metadata. A model change preserves account
  identity and drops unsupported effort. Muse's supported choices are available;
  an admin-managed account does not force the project to use the default effort.
  Cursor Auto/Composer have no separate control; Antigravity uses model variants.
- Cursor model effort now passes through `mcpagent` and both CLI transports as
  native `[effort=...]` parameters, preserving context and speed settings. Unknown
  live model IDs retain their exact selectors. GPT-5.5/GPT-5.4 are removed from
  selectable Codex models; metadata remains for existing saved sessions.
- Cursor's curated GLM/Grok choices and its CLI live list are merged without
  duplicate IDs. Remaining inventory and cost gaps have separate tickets:
  [PLAT-387](plat-387.md), [PLAT-388](../cost-telemetry/plat-388.md).

The final setup/effort implementation is AgentWorks `e5f25e3d7`, mcpagent
`7effecb` and llm-provider-mcp `6baebb0`. Earlier provider UI/catalog fixes are
also on main (`4a5b7820b`, `18617827a`, `d98903c99`, `f18606520`, `dca64cc0b`,
`c5c6f92d4`; provider `a8a1445`, `c37b65a`).

## Verification

31 focused frontend tests passed, including credential-field absence across
product scopes, collapsed-model effort controls, account/model preservation and
unsupported-effort reset. Frontend type checks, embedded Crew/Code profile
checks, Cursor agent integration and CLI-argument tests, and commit lint/secret
checks passed. CLI-argument verification uses a fake subprocess and proves the
selected effort reaches launch after a model override; it is not live inference.

RTS CLI-only probes on 2026-10-03 used the existing service key, service UID,
disposable HOME/workspace and a no-tools Ask prompt. Installed Cursor CLI
`2026.10.01-e373342` matched the [official installer](https://cursor.com/install)
at that time. Grok 4.6 inference succeeded. GLM 5.3/Flash were unavailable to this
key. The probes did not deploy/restart the service or update its CLI/accounts.

## Catalog references

The original catalog update used Cursor's official model pages:
[GLM 5.3](https://cursor.com/docs/models/glm-5-3),
[GLM 5.3 Flash](https://cursor.com/docs/models/glm-5-3-flash), and
[Grok 4.6](https://cursor.com/docs/models/grok-4-6). These describe model support;
the account-qualified inference result is recorded separately above.

## Rollout record

[PLAT-382](../browser/plat-382.md) records RTS release
`f69f9fd-20261003144322`. Git ancestry confirms its AgentWorks source includes
`e5f25e3d7` and the earlier provider UI fixes. That deployment record qualifies
browser work; it does not establish a live product setup/effort acceptance result.

## Left

- Verify product setup screens and a saved effort change through an actual app
  turn on the recorded RTS release or a newer release. Check the deployed
  source/dependencies before marking other hosts deployed. Source inclusion and
  CLI-only probes are not a full coding-loop acceptance test.
- Resolve account-aware Cursor inventory and native pricing qualification in the
  linked open tickets. Existing project credentials are preserved for compatibility;
  this ticket does not claim a credential migration.
