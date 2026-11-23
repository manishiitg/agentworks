# Cross-Repo Integration Contract (multi-llm-provider-go)

> **The canonical cross-repo contract has moved.** It now lives in
> `mcp-agent-builder-go/agent_go/docs/cross_repo_integration_contract.md`
> and is the single source of truth for the 3-repo LLM pipeline.

```
mcp-agent-builder-go (HTTP API + frontend)        ← canonical contract lives here
  → mcpagent (orchestrator + agent loop)
    → multi-llm-provider-go (this repo — bottom layer)
```

## What this repo owns

Adapter-level contracts that the canonical document REFERENCES but does
not duplicate:

| Document | Scope |
|---|---|
| `docs/api_provider_test_contract.md` | 24-area test matrix for API adapters (plain text, streaming, tool use, sampling, image, PDF, thinking, JSON, prompt caching, auth, rate limit, ...). Tracks per-provider coverage. |
| `docs/coding_sdk_structured_contract.md` | Per-provider structured-mode (`--print` / `--exec` / `stream-json`) transport contract |
| `docs/coding_sdk_tmux_contract.md` | Per-provider tmux interactive transport contract |
| `docs/CODEX_CLI_CODING_AGENT_CONTRACT.md` | Codex CLI-specific behavior (exec mode, session storage, etc.) |

## Where to look for cross-repo concerns

- **Cost tracking** (USD, effective model, ledger flow) → canonical doc, "Cost Tracking Contract" section
- **Inspector debug** (opt-in event sink, phases, store) → canonical doc, "Inspector Debug Contract" section
- **Integration areas IC-1 through IC-10** → canonical doc
- **Boundaries between mcp-agent-builder-go ↔ mcpagent ↔ this repo** → canonical doc

When in doubt, the **canonical doc is the truth**. Update there first; only
update this file when the change is purely adapter-internal.

## Inspector contract matrix

The cross-adapter contract enforcement test lives in this repo at
`inspector_contract_matrix_test.go`. Adding a new adapter to the inspector
means registering it in `inspectorContractFactories` — the matrix test
then runs the canonical assertion against it. See the "Inspector Debug
Contract" section of the canonical doc for the required shape.
