[← coding-agents / codex](index.md)

# PLAT-416 — Codex `gpt-5.3-codex-spark` refused for ChatGPT accounts; allowed-models 422 on a stale saved model

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | coding-agents |
| Area | codex |
| Summary | fixed on `main`; deploy pending. |

| Coordination | Value |
|---|---|
| State | fixed on `main`; deploy pending (Excellence and the other servers) |
| Severity | P2 (a chat failed with a 422 although the agent had answered) |
| Date | 2026-10-04 |
| Owner | coding-agent-bridge |
| Related | PLAT-407 (allowed models per account) |

## Problems

1. Codex signed in with a ChatGPT account answers `The 'gpt-5.3-codex-spark' model is not supported when using Codex with a ChatGPT account.` The platform still offered Spark: the provider's visible Codex catalog,
   the auto-published fast models, the web-search provider list, and the image analysis and generation lists.
2. On Excellence the Admin-managed Codex account was restricted to `gpt-6-luna`. A Code chat whose project still had Spark saved answered correctly (the terminal showed GPT-6 Luna) but the chat showed
   `Request failed with status code 422: gpt-5.3-codex-spark is not allowed on Admin-managed account; allowed: gpt-6-luna`.

## Cause (2)

`constrainProductChatModel` treated a model that differed from the conversation's bound model as a new pick and refused it. After the first turn fell back to Luna the conversation was bound to Luna,
while the browser kept re-sending the saved Spark, so every later message counted as a new pick of a disallowed model.

## Done

- Spark is gone from the visible Codex catalog (multi-llm-provider-go `daffefd`; its pricing metadata stays so a saved selection still resolves), from the auto-published models (now `gpt-5.4-mini` only), and from the
  web-search, image analysis and image generation lists. Contract tests and docs use `gpt-5.4-mini`. `agent_go/go.mod` and `go.sum` pin that provider commit.
- `constrainProductChatModel` never refuses: a model the account does not allow runs on the first allowed one (logged). `TestAllowedModelsProductChatTurn` covers a picked and a re-sent model, no model, and an account without a list.

## Left

- Deploy. The Code project that had Spark saved keeps it until its next turn; it then runs on the allowed model.
- The browser still shows the saved model in its picker; syncing the shown selection to the allowed one when the account's list loads is not done.
- The Codex tier shortcuts High, Medium and Low (`high` is the Codex default model) appear next to the concrete models they resolve to (High = GPT-6.1 Sol, Medium and Low = GPT-6 Luna), so a model shows twice
  in the catalog. They are the tier system the defaults and a test rely on, so they were not hidden; an allowed list of a concrete model does not cover its shortcut (a pick of `low` is not matched against `gpt-6-luna`).
- A failing `pkg/codingagentmodels` test in the provider repo (a retired Cursor model still listed) fails on a clean `main` too.

## Register notes

[PLAT-416](plat-416.md), P2, fixed on `main`; deploy pending. A model the account does not allow now runs on the first allowed one instead of failing the chat.
