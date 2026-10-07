[← chat / rendering](index.md)

# PLAT-667: Show web searches as a search card in the chat

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | chat |
| Area | rendering |
| Summary | A coding agent's web search now shows as a collapsed "Searched: <query> · N results" card with clickable result rows, instead of a generic tool call with raw arguments. |

## What happened

Web searches by coding agents rendered as an ordinary tool call inside the
collapsed "N tool calls" chip, with the query only visible as raw JSON and the
results as one long text blob. The owner asked (2026-10-07) for a search card.

What each agent actually reports (checked against real recorded calls):

- **Claude Code** `WebSearch`: args `{"query","mode"}`; the result text is
  `Web search results for query: "…"`, then `Links: [{"title","url"},…]`, then a
  summary. (salesoutreach step `research-and-draft-outreach`, 9 searches.)
- **Cursor** `WebSearch`: args `{"search_term","explanation"}`, sometimes inside a
  `CallDynamicTool` wrapper `{"toolName":"WebSearch","namespace":"cursor","arguments":{…}}`;
  result `Title: Web search results\nContent: Links:\n1. [title](url)…`
  (from `~/.cursor/chats/*/store.db`; our transcript stream forwards these).
- **Codex** `web_search`: before the provider fix the structured start event
  had empty args and the end event no args and no result (the query was only on
  the adapter's end chunk, which mcpagent does not put on `tool_call_end`), and
  the tmux transcript stream skipped search rows. What Codex records (checked
  live on codex-cli 0.160.1, 2026-10-07):
  - `exec --json`: `item.started` `{"type":"web_search","query":"","action":{"type":"other"}}`;
    `item.completed` adds the query, `action` (`search` with `query`/`queries`,
    or `open_page`/`openPage` with `url`, or `find_in_page` with `url`,`pattern`)
    and `results: [{"title","url","domain","snippet",…}]`.
  - rollout: `event_msg` `item_completed` `{"type":"Extension","kind":"web.search",
    "query","action","results"}` with `started_at_ms`/`completed_at_ms` (no start
    row); older builds wrote `{"type":"WebSearch","query","action"}` with no results.
    A failed page open has a result with a title but no URL.
- **AGY** `search_web`: args `{"query"}`; the end event carries text only when
  the call fails (the adapter reads tool steps from the conversation DB, which
  holds no result text). No recorded AGY search was found locally.

## Fix

- `frontend/src/utils/webSearchToolCall.ts` parses a call into query, result
  rows (Claude `Links:` JSON, a JSON `results` list, markdown links, then bare
  http(s) links) and opened pages (a Codex "query" that is a URL). Only http(s)
  links become rows.
- `WebSearchToolCallDisplay` (ToolCallSpecialRender) is the card: one compact
  row, collapsed by default, rows open in a new tab; theme tokens, truncates at
  narrow widths.
- Chat transcript (`TerminalEventTranscript` `ToolBatch`): searches are taken
  out of the "N tool calls" chip and shown as their own cards in order; the
  start/end pair is joined by tool call id (existing `pairToolCalls`). Same path
  for live chats and restored history.
- `ToolCallStart/EndEvent` (EventDispatcher) and the step-log viewer
  (`ConversationViewer` tool responses and the call timeline) use the same card.
- Codex (multi-llm-provider-go 585c887, `codexcli_web_search.go`): both the
  structured adapter and the tmux transcript stream read those structured
  fields. Args are `{"query", "action"}`; the end result is
  `Web search results for query: "…"` plus `Links: [{"title","url"}]`, the
  Claude shape the card already parses, so the card shows the query and the
  sources even where only the end event's result reaches it. An opened page
  shows as opened (its URL is the query). No pane or screen parsing.

## Verification

- Vitest `src/utils/webSearchToolCall.test.ts` (Claude, AGY, Codex, Cursor
  shapes from real data; the Codex case uses the provider's output for a real
  0.160.1 search); provider test `TestCodexWebSearchCarriesQueryAndSources`
  (real exec event and rollout row) plus the existing transcript and tool-end tests, run
  on GitHub via `scripts/verify-remote.sh`.
- Not checked live in the browser (laptop build/test rule). After the next
  app restart: open a Claude chat that runs a web search, or the salesoutreach
  run's `research-and-draft-outreach` step log.

## Left

- Codex: not yet seen live in the chat (needs the next app restart/deploy); run
  a Codex chat that searches the web and check the card shows the query and
  sources. Snippets are dropped (the card shows title and link only).
- AGY: no search result text reaches the event stream; the card shows the
  query only.
