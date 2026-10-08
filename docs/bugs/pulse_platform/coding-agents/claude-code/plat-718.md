[← coding-agents / claude-code](index.md)

# PLAT-718: Claude Haiku 5.5 replaces Haiku 4.5

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | coding-agents |
| Area | claude-code |
| Summary | Claude Haiku 5.5 (claude-haiku-5-5) replaces Haiku 4.5 on the platform |

## What happened

## Fix

## Left

## Why

Anthropic released Claude Haiku 5.5 on 2026-10-07 (`claude-haiku-5-5`, 1M context, adaptive thinking). Up to 100K-token prompts it costs $0.10 in / $0.50 out / $0.01 cache read / $0.125 cache write per 1M, a tenth of Haiku 4.5; over 100K, $0.50 / $2.50. Owner (2026-10-08): add it and replace Haiku 4.5.

## Done

- multi-llm-provider-go `faee815`: the Claude Code model list offers Haiku 5.5 instead of 4.5; saved `claude-haiku-4-5` / `claude-haiku-4-5-20251001` selections run and are priced as Haiku 5.5 (same mechanism as Sonnet 5 → 5.5). Test `TestRetiredClaudeModelsRunAsTheirReplacement`.
- agent_go: pins it; the manifest fallback list, the Claude sign-in check (`claude -p hi --model …`, `claudeauth.verifyModel`), Claude Code pricing aliases (`haiku`, `haiku-4-5` → Haiku 5.5), the test tools and the Claude Code real-e2e defaults use `claude-haiku-5-5`.

## Left

- Metadata lists the up-to-100K price only; long prompts are under-priced by 5x until the cost ledger knows the tier.
- The direct Anthropic API path (unused, coding CLIs only) still knows Haiku 4.5.
- Not deployed.
