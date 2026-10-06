[← coding-agents / claude](index.md)

# PLAT-479 — Clarification choices as cards for every attended coding chat, with Claude's native AskUserQuestion (PR 270)

| Field | Value |
|---|---|
| State | deployed |
| Priority | - |
| Product | coding-agents |
| Area | claude |
| Summary | fixed on `main` (PR 270 plus its review fixes), needs a restart: selectable cards for clarification questions in every attended coding chat; Claude's native question uses the same lifecycle; verified live. |

| Coordination | Value |
|---|---|
| State | fixed on `main`; needs a rebuild and restart |
| Date | 2026-10-04 |
| Owner | coding-agent-bridge |
| Related | PLAT-473 (native tool policy), PR 270 |

## Source

Workflow chat showed a coding agent's clarification options as static text under an
agent-specific label. PR 270 renders selectable cards in the workflow and chat
transcripts, registers a shared `request_clarification` tool for attended, writable
coding-agent chats, and routes Claude's native `AskUserQuestion` through the same
answer lifecycle (answers validated and durably settled before delivery; cancelled or
stale prompts close without assuming a choice). Muse's native delivery is unchanged.

## Review (read-only, then fixed by the owner's session)

A reviewer found the lifecycle sound (answers validated, then durable, then delivered;
two answers at once get a 409; a user cannot answer or read another user's prompt;
scheduled, bot, child and read-only sessions do not get the tool) and these issues:

- **Dependency pins pointed at unmerged branches** (mcpagent `a639bf5`, provider
  `b92093d`). Fixed: the provider commit was rebased onto main (`2ccfe00`), the
  mcpagent commit merged with the fixes below (`9b5da2b`), and the builder pins both.
- **Claude hook could fail the whole turn** when `python3` is missing or the bridge has no
  session credentials. Fixed: native questions are left off for that turn
  (`AskUserQuestion` not offered) and the turn runs.
- **Hook scripts (which hold a session token) were never removed.** Fixed: scripts older
  than 24 hours are swept.
- **Option labels with surrounding spaces were denied** (the server trims labels, the hook
  did not). Fixed: the hook trims them.
- **A question lost in a restart stayed answerable in the clean conversation view**
  (the transcript view already closed it). Fixed: one shared `withClosedQuestions` used
  by both views.

- **CI: the new `clarification` prompt section was not declared** in the three
  `product.yaml` files, which the product-surface contract tests check (AgentWorks and
  Crew failed). Declared in the AgentWorks, Crew and Code product files next to
  `native-subagents`; it still applies only to attended builder chats.

## Done / verified

- Targeted Go tests (Clarification, ClaudeNativeQuestion, PromptSections) and 15 frontend
  tests pass; `tsc -b` clean.
- `TestClaudeNativeQuestionLive` against real Claude Code (warm session, then the hook
  answers a two-question AskUserQuestion; history survives): pass, 41 s.

## Left

- After a page reload the closed set is empty, so a lost question shows pending once;
  one click closes it again. The server cannot settle a prompt it no longer holds.
- Not run: Claude in Full CLI as a per-user server account or under the Mac sandbox;
  Codex's tool timeout against the 30-minute wait; Muse and Cursor with
  `request_clarification`. Cursor's native `AskQuestion` is not blocked (no hook fires
  for it) and a headless Cursor can still stall on it.
- The `header` length is only in the tool schema, not enforced in
  `parseCodingAgentClarification` (low).

## Register notes

[PLAT-479](plat-479.md), fixed on `main` (PR 270 plus its
review fixes), needs a restart: selectable cards for clarification questions in every
attended coding chat; Claude's native question uses the same lifecycle; verified live.
