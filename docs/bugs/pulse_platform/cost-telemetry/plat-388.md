[← Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-388 — Cursor native model defaults and suffix IDs need pricing resolution

| Coordination | Value |
|---|---|
| State | open; native selector mismatch reproduced on RTS |
| Date | 2026-10-03 |
| Owner | cost-telemetry; metadata resolution in coding-agent-bridge |

## Evidence

With RTS's existing key, bare `grok-4.6` reports `Grok 4.6 High Fast` and
`cursor-grok-4.6-high` reports `Grok 4.6 High`. Current curated metadata assumes
standard pricing unless a selector explicitly contains `fast=true`. Live suffix
IDs also need family-aware metadata. Explicit native parameters are preserved,
but catalog pricing alone does not certify the runtime's actual mode or bill.

## Left and acceptance

Resolve native aliases/suffixes and actual speed mode before cost attribution;
retain explicit context/speed/effort parameters. Keep inferred or unsupported
pricing visibly estimated instead of presenting it as exact. Cover bare Grok,
explicit Fast/non-Fast selectors and live suffix IDs with deterministic tests
and account-qualified CLI evidence before certifying cost attribution.

[PLAT-386](../frontend-chat/plat-386.md) tracks completed Cursor effort forwarding;
that fix does not close this pricing issue.
