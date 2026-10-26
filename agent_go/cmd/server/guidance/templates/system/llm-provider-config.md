## LLM Provider Config - published chat models and provider auth

Published LLMs and provider credentials are managed by tools. Never inspect or edit `config/` files with shell or file tools.

### Which tool to call

- User asks "which LLMs do we have?" or "what is published?" -> call `models action=list scope=project`.
- User asks "what models can this provider publish?" -> call `models(action="catalog", provider="...")`.
- User asks whether a candidate works -> call `models(action="test", provider="...", model_id="...", options={...})`.
- User asks to publish or update an LLM -> call `models action=test` first when practical, then `save_published_llm`.
- User asks to add/change credentials -> call `set_provider_auth`; never paste keys into shell commands or config files.

### Minimal published LLM shape

Store only the routable model identity and model-specific options (the `name`/`model_id` below are an **example** — use a current model id from `models action=capabilities` / `models action=catalog`, not a hardcoded version):

```json
{
  "name": "Claude Opus 4.1 high",
  "provider": "claude-code",
  "model_id": "claude-opus-4-1",
  "options": {
    "reasoning_effort": "high"
  }
}
```

`id` and `created_at` may be assigned by the system. `options.reasoning_effort` is the field that preserves low/medium/high reasoning for providers such as Claude Code and Codex CLI. Do not invent or persist `context_window`, pricing fields, `auth_method`, `temperature`, or tool-call settings unless a dedicated tool explicitly asks for them.

### Correct publishing flow

1. Call `models(action="catalog", provider="...")` and use the returned model id / supported reasoning levels.
2. Call `models action=test` with the exact provider, model id, and any `options.reasoning_effort` the user requested.
3. Call `save_published_llm` with `name`, `provider`, `model_id`, and optional `options`.
4. Call `models action=list scope=project` to verify the saved entry.

### Code-execution bridge pattern

When only `execute_shell_command` is directly exposed, call custom tools through `$MCP_CUSTOM`:

```bash
payload='{"action":"list","scope":"project"}'
curl -sS --json "$payload" -H "$MCP_AUTH" "$MCP_CUSTOM/models"

payload='{"action":"catalog","provider":"claude-code"}'
curl -sS --json "$payload" -H "$MCP_AUTH" "$MCP_CUSTOM/models"
```

Use the same pattern for `models action=test`, `save_published_llm`, and `set_provider_auth`.
