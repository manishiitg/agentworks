[← relays / execution](index.md)

# PLAT-611: Execute Python Relays with fresh agent calls and custom tools

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | relays |
| Area | execution |
| Summary | Python API programs reuse core agents and shared Relay infrastructure without workflow plans or agent recovery. |

## What happened

The owner requested a simpler Relay: Python owns agent calls and branching, with
custom prompts/tools and no resume agent in MVP. Graph/plan execution couples
this product to workflow contract migrations and unnecessary step machinery.

## Fix

New Relay creation writes `relay.py` and a Python runtime discriminator. The SDK
provides fresh `call_agent`, ordered messages, Python callable tools, explicit
skills/MCP, direct `call_mcp`, selected Vault secrets and flat configuration.
The shared sandbox runs it; the shared core agent and signed bridge execute
calls. Existing trigger admission, idempotency, access, live credential checks,
publishing, cost observer, execution tracker and polling are reused.

The Builder product manifest, prompt, skill and slash commands teach Python.
The right pane shows exact Source and recorded Calls/returned JSON, including
published-version runs. File editing preserves Python escapes/whitespace.
Legacy graph Relays stay on the previous runtime; no migration is introduced.

Design and verification scope: [Python Relays MVP](../../../../design/python_relays.md).

## Verification

Real Python protocol chain and failure checks passed. Published starter executed
through real workspace handlers and preserved release hashes. Frontend TypeScript
and focused Source/Calls/file loading tests passed. Shared Relay/function/webhook regressions passed. Real Codex acceptance passed:
a Python closure executed through the signed tool bridge and a second message
returned its exact code as schema-validated JSON. That live test caught and
fixed callback writes using the child agent guard instead of the parent invocation.

## Left

Recovery, resume-agent and per-release Python
environments are explicitly outside this MVP. External MCP/Google operations
require live authorized connections and are not all exercised by these checks.

## Shared suite follow-up

The initial focused checks missed the shared landing JSX assertion. That
regression is fixed in [PLAT-627](../frontend-chat/plat-627.md). The current main
Relay backend and relevant frontend checks pass; unrelated main suite failures
are tracked in [PLAT-626](../../app/tests/plat-626.md).
