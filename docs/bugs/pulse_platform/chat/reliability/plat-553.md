[← chat / reliability](index.md)

# PLAT-553 — The chat never showed when a coding CLI compacted its context

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | chat |
| Area | reliability |
| Summary | fixed on main, not deployed: Codex, Claude, Pi, Muse and Agy compaction records become a `context_compaction` event and one chat row ("Compacting context…" → "Compacted context (1m 39s) · 384k → 92k tokens"); Cursor rec… |

| Coordination | Value |
|---|---|
| State | fixed on main (2026-10-06); not deployed |
| Date | 2026-10-06 |
| Owner | chat-reliability |

## Source

Owner request: show context compaction for every coding CLI in the chat, from structured records only ("show, don't scrape", PLAT-354 list). Before this, nothing in llmtypes, mcpagent or agent_go carried compaction; a long turn just went quiet for minutes.

## Done

- Provider (`multi-llm-provider-go` 6ea681c): new chunk `context_compaction` with a `ContextCompaction` payload (phase start/end, id, trigger, outcome, tokens before/after, started/ended, duration). Per CLI:
  - Codex: rollout `item_completed` ContextCompaction = end with started/completed ms. Tokens before = the last `token_count` input; tokens after = the zero-input `token_count` Codex writes right after compacting (seen on real 0.160 rollouts: 235118 → 20754). Tmux tailer and a new rollout side channel for the structured transport (`codex exec --json` has no compaction item).
  - Claude: stream-json `system/status` "compacting" (start), `compact_boundary.compact_metadata` and `status null + compact_result` (end); transcript `compact_boundary.compactMetadata` (end, tmux). Shapes from the 2.1.289 bundle and a real 2.1.226 transcript.
  - Pi: `--mode json` `compaction_start` / `compaction_end`; tmux marker extension hooks `session_before_compact` / `session_compact` / `session_compact_failed` (writes reason, tokensBefore, aborted only).
  - Muse: `context_compaction_candidate` running (start), `context_compaction_installed` (end, budget before/after = Muse's estimated prompt tokens, summarizer duration). `tool_results_cleared` is a light prune and is not shown.
  - Agy: finished CHECKPOINT steps (type 23) that are not `intent_only` (end, start/end from step metadata). Every real conversation has one intent-only checkpoint that only titles the chat; those are skipped.
- mcpagent (5a9ec3e): `ContextCompactionEvent` (`context_compaction`), emitted even when generation streaming events are suppressed.
- agent_go: event in schema-gen, the durable chat allowlist and STRUCTURAL_EVENTS (survives reload). Frontend: one quiet row per compaction at the start's position: "Compacting context…" (spinner), then "Compacted context (1m 39s) · 384k → 92k tokens"; unknown parts omitted; a start left open by a finished turn reads "no result recorded".
- Tests: one fixture test per CLI parser from real record shapes (Codex, Claude transcript, Muse, Agy from real files; Pi from Pi's docs/types, no real Pi event stream on this laptop), and a vitest for the row collapse.

## Judgment calls

- Consumers pair start/end by `compaction_id` and keep the newest end, so Claude can emit an end on `compact_result` and again on the later boundary (order undocumented) without two rows.
- Codex and Agy have no start record, so they show only the finished row.
- The compact chat view (older turns as messages only) drops compaction rows of older turns; the latest turn and the full view keep them.

## Left

- Cursor: no structured compaction in stream-json or transcripts, only a `preCompact` hook (start only, no end). Not shown.
- Retained live-input follow-up turns read progress as messages, not chunks, so a compaction there is not shown (Codex, Claude, Muse, Agy).
- Not seen live: no compaction was triggered during this work (needs a very large context). Parsers are verified on recorded records; the event plumbing is shared with live usage (PLAT-554), which was checked live.

## Register notes

[PLAT-553](plat-553.md), P2, fixed on main, not deployed: Codex, Claude, Pi, Muse and Agy compaction records become a `context_compaction` event and one chat row ("Compacting context…" → "Compacted context (1m 39s) · 384k → 92k tokens"); Cursor records none.

## Verified live

2026-10-06: in the owner's local app a long Plan Drift chat showed "Compacted context (2m 10s) · 236k → 24k tokens".
