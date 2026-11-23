# Providers And Models

The MCP product exposes four non-deprecated local coding-agent targets. Each
target uses its own installed CLI and existing authentication.

| Provider ID | CLI | Authentication | Typical use |
|---|---|---|---|
| `cursor-cli` | Cursor Agent | Cursor account login | Composer and account-visible models |
| `pi-cli` | Pi | Provider keys managed by Pi | Gemini, OpenRouter, MiniMax, GLM, Kimi |
| `codex-cli` | Codex CLI | `codex login` | OpenAI coding models and reasoning levels |
| `claude-code` | Claude Code | `claude` login | Claude Code models and native tools |

Run the local catalog command before choosing a model:

```bash
llm-provider-mcp models cursor-cli
llm-provider-mcp models pi-cli
llm-provider-mcp models codex-cli
llm-provider-mcp models claude-code
```

Add `--json` for machine-readable output. Cursor also supports `--live` to query
the current account-visible model catalog.

## Cursor Agent

Omitting the model currently selects `composer-2.5`. `grok-4.7` is an exact
Cursor model ID. Other exact Cursor IDs pass through unchanged.

Cursor model availability belongs to the current Cursor account and can change
independently of this project. Run `cursor-agent models` or the live catalog
command when an exact selector is rejected.

## Pi

The curated catalog keeps one current model per supported family, with separate
Gemini Flash and Pro tracks:

- `google/gemini-3.8-flash`
- `google/gemini-3.5-flash-lite`
- `google/gemini-3.1-pro-preview`
- `minimax/MiniMax-M3`
- `zai/glm-5.3`
- `moonshotai/kimi-k3`

Pi also accepts dynamic OpenRouter selectors such as
`openrouter/moonshotai/kimi-k3`. Use `openrouter/openrouter/free` to let
OpenRouter select an available free model. Dynamic OpenRouter models are not
hardcoded because that catalog changes independently.

Pi credentials remain in Pi's own configuration. The setup wizard never asks
the user to paste provider API keys into `llm-provider-mcp`.

## Codex CLI

Codex accepts model IDs and reasoning levels exposed by the installed CLI. Use
the catalog instead of assuming that an account has access to every advertised
model.

The curated catalog includes GPT-6 Astra, Sol, and Luna. Exact availability
depends on the installed Codex build and account.
Published standard token rates are recorded in the model metadata; requests
above 272k input tokens use the published long-context multipliers.

Codex delegated jobs run with `approval_policy=never` and the `workspace-write`
sandbox so a detached job cannot wait on an invisible approval prompt.

## Claude Code

Claude Code accepts its native model selectors and uses project-scoped tool
permissions for detached jobs. Setup checks the existing Claude authentication
status and can open the native login flow when needed. The curated catalog
includes `claude-opus-5-5`.

## Cost estimates

Model metadata uses published standard token rates. Cursor Grok 4.7 has
separate Fast pricing: select `grok-4.7[fast=true]` when requesting Fast so the
cost estimate uses those rates. Cursor may enable Fast by default based on the
account plan, which cannot be inferred from a plain `grok-4.7` selector.
Grok 4.7 requests above 256k input tokens use long-context rates. Estimates
from interactive Cursor sessions also rely on approximate token counts.

## Removed Providers

Gemini CLI has been removed. Use Pi for Gemini model routing or Vertex for
direct Gemini API access.
