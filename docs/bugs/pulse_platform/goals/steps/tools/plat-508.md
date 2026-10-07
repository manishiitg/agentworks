[← goals / steps / tools](index.md)

# PLAT-508 — Remove the `search_web_llm` platform tool; coding agents use their own web search

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P0 |
| Product | goals |
| Area | steps/tools |
| Summary | fixed on main, not deployed. |

| Coordination | Value |
|---|---|
| State | fixed on main; not deployed |
| Date | 2026-10-05 |
| Owner | step-execution |

## Source

Owner decision 2026-10-05: "remove this tool, search web llm; coding agents can use their own". Every agent is a coding CLI with its own native web search. The tool only called the anonymous free-tier hosted MCP search
services (Parallel, Exa, Firecrawl) and hit their rate limits: `You've hit the free-tier rate limit for Parallel Search MCP`.

## Done

- Removed the tool definition, executor, the hosted-MCP request builders, the `search_web` capability in `list_llm_capabilities`, the profile/feature-flag/tool-list entries, the `agentworks testing` provider command, the P0 script, the live tests and testdata, and the frontend name mapping.
- Guidance (`workspace-media-tools`, special-tools instructions, provider-config text) no longer names it. The Video Studio prompt now says to use the agent's own native web search tool for research.

## Left

- Existing workflows (instagram, jobsearch, linkedin, social-media, substack, websiteaeo `planning/step_config.json`) keep a stale `search_web_llm` in steps' `enabled_custom_tools`, and a few plan texts mention it. This is harmless: `enabled_custom_tools` is only a filter against the real tool pool, and plan validation checks the `category:` prefix, never the tool name. A Builder cleanup is optional.
- `internal/agentsession` keeps its own separate `exa-search` MCP config (an SDK session); left on purpose.

## Register notes

[PLAT-508](plat-508.md), fixed on main, not deployed. The tool only wrapped free-tier hosted MCP search that hit rate limits. Existing workflows keep a harmless stale name in `enabled_custom_tools`.

## Follow-up (2026-10-07): AGY and Pi in workflow steps

Claude Code (WebSearch), Codex (web search) and Cursor (auto-approved) keep their own web search in mcp_only steps.
AGY's mcp_only hook denied every native tool, including its own `search_web`, so an AGY step had no web search at
all; multi-llm-provider-go 3e1cdef allows `search_web` there (every other native tool stays denied), and the builder
pins it. Pi has no native web search; steps that need research should run on Claude, Codex, Cursor or AGY.

