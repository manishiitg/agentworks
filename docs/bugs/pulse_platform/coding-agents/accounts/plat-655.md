[← coding-agents / accounts](index.md)

# PLAT-655: Model picker offers providers a person cannot use

| Field | Value |
|---|---|
| State | open |
| Priority | P1 |
| Product | coding-agents |
| Area | accounts |
| Summary | Non-admins on Excellence could pick (and new projects defaulted to) Codex/Claude Code, which are admins-only there; every turn was then refused |

## What happened

## Fix

## Left

## What happened

Excellence, 2026-10-06/07: many users (Vishwas and others) got "Request failed with status code 403 / the codex-cli server account is not available to you here (available to: Admins only)" even after picking Muse. Provider policy there: claude-code and codex-cli admins-only, muse-cli for everyone. Six Code projects of six non-admins had codex-cli saved as their model (workflow.json llm_config), and the chat query uses the project's saved model, so every turn was refused (34 Code + 25 Crew refusals). No model save of theirs reached the server, so their Muse choice never replaced the saved Codex. New Code projects default to Claude Code, also admins-only there.

## Fix

- The project Models panel offers only coding agents with a usable account for this person and product; when the saved one is not usable it says so and asks to choose another.
- A new Code/Crew project starts on the product default only if the person may use it, else the first usable coding agent.
- The refusal now says where to change it.

## Left

- Find where users "selected Muse" without it saving (likely a chat-tab-only control); not reproduced.
- The six projects already on Codex switch once their owner picks Muse in the Models panel (or an admin opens Codex to everyone).
