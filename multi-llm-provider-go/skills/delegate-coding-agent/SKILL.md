---
name: delegate-coding-agent
description: Route coding work to another local coding-agent CLI through the llm-provider MCP server. Use when a task benefits from a powerful model for difficult reasoning or a faster, lower-cost model for bounded implementation, tests, investigation, or review.
---

# Delegate Coding Agent

Choose the smallest capable model, delegate a precise task, and verify the result in the host session.

## Choose a Route

Call `list_coding_agents`, then call `list_coding_agent_models` for the selected provider. Treat the live catalog and the user's preference as authoritative; model names and account availability can change.

| Route | Use for | Current examples |
| --- | --- | --- |
| Powerful | Ambiguous architecture, cross-module debugging, security-sensitive work, difficult planning, or final review of high-risk changes | Codex `gpt-6-astra` or `gpt-6.2-sol`; Claude `claude-opus-5-5`; Pi `google/gemini-3.1-pro-preview` or `zai/glm-5.3` |
| Balanced | Normal feature work, refactors, tool-heavy implementation, bug investigation, and tests | Codex `gpt-6.2-sol`; Claude `claude-sonnet-5-5`; Cursor `composer-2.5`; Pi `google/gemini-3.8-flash`, `minimax/MiniMax-M3`, or `moonshotai/kimi-k3` |
| Fast | Mechanical edits, focused UI iteration, simple test fixes, repository search, summaries, and other bounded work with objective checks | Codex `gpt-5.4-mini`; Claude `claude-haiku-4-5-20251001`; Pi `google/gemini-3.5-flash-lite` |

Use these as routing hints, not benchmark rankings. Prefer `auto` when the user has no model preference and provider-native routing is more useful than a fixed model.

## Delegate

1. Decide whether delegation is worth the startup and verification overhead. Keep tiny tasks in the host session.
2. Select a provider and model from the live tools. Use a powerful route when requirements or correctness are uncertain; use a balanced or fast route only when the task is bounded and independently verifiable.
3. Give `delegate_coding_agent` one clear outcome. Include the allowed scope, constraints, non-goals, relevant verification commands, and the expected response.
4. Pass the current trusted project root as `working_dir` automatically. Do not ask the user to enter it.
5. Record the returned `job_id`. The delegation is asynchronous.
6. Call `get_coding_agent_job` only after `poll_after_seconds`. Continue until the status is `completed`, `failed`, `cancelled`, or `timed_out`.
7. Keep ordinary polling lightweight. When the user asks what is happening or progress appears stale, call `get_coding_agent_job` with `include_terminal_output: true` to receive the bounded plain-text tmux tail. Use `tmux_capture_command` only for deeper history.
8. Read the terminal result, inspect the actual diff, and run relevant tests before reporting completion.

## Write a Delegation Task

Use this shape:

```text
Outcome: <one concrete result>
Scope: <files or subsystem the agent may change>
Constraints: <behavior to preserve, dirty-worktree rules, and non-goals>
Verify: <specific tests, checks, or observable result>
Return: <short summary, changed files, and verification output>
```

For fast models, make the scope especially narrow and provide deterministic checks. Use a powerful model to resolve unclear requirements or review risky work before assigning a bounded implementation to a faster model.

## Handle Problems

- Respect `poll_after_seconds`; do not poll continuously.
- If a job appears stalled, request `include_terminal_output` before using the deeper `tmux_capture_command` or cancelling it.
- Never run `tmux_attach_command` from the host agent; it is interactive and intended for a human terminal.
- If a job fails or times out, read its error, reduce the task scope, and retry once with `auto`, a stronger model, or another installed provider.
- Cancel only when the work is no longer needed or cannot make useful progress.
- Do not recursively ask a delegated agent to delegate again.
- Preserve existing user changes and never treat a provider's text response as proof that its edits or tests are correct.
