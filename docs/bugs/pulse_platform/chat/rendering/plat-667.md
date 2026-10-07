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
- **Codex** `web_search` (structured adapter): the start event has empty args,
  the end event carries no args and no result. The query exists only on the
  adapter's end chunk, which mcpagent does not put on `tool_call_end`, so in
  recorded runs (websiteaeo, jobsearch) the call has a name and duration only.
  Codex's own rollouts hold more (`Extension`/`web.search` items with a result
  list, `WebSearch` items with `open_page` URLs), but neither the structured
  nor the tmux-transcript path forwards them today.
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

## Verification

- Vitest `src/utils/webSearchToolCall.test.ts` (Claude, AGY, Codex, Cursor
  shapes from real data) plus the existing transcript and tool-end tests, run
  on GitHub via `scripts/verify-remote.sh`.
- Not checked live in the browser (laptop build/test rule). After the next
  app restart: open a Claude chat that runs a web search, or the salesoutreach
  run's `research-and-draft-outreach` step log.

## Left

- Codex: carry the end-event args (query) onto `tool_call_end`, and map Codex
  `Extension`/`web.search` and `WebSearch` rollout items (results, `open_page`)
  in multi-llm-provider-go, so Codex searches show their query and results.
- AGY: no search result text reaches the event stream; the card shows the
  query only.
