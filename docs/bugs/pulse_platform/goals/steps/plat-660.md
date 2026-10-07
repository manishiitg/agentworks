[← goals / steps](index.md)

# PLAT-660: Steps still name the removed search_web_llm tool

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | goals |
| Area | steps |
| Summary | Workflow Review flags a step that still names a removed platform tool (search_web_llm), from its text or enabled_custom_tools |

## What happened

PLAT-508 removed `search_web_llm` (agents use their own web search) but left the name in older workflows' step text
and `enabled_custom_tools` (instagram, jobsearch, linkedin, social-media, substack, websiteaeo, salesoutreach). The
tool list entry matches nothing, but the text still tells agents to use it: a Codex step in sales outreach asked
`get_api_spec` for `search_web_llm` before searching on its own (2026-10-07). A weaker agent could conclude it has no
web search.

## Fix

The reference map reports a `removed_tool` break for a step whose text or `enabled_custom_tools` names a removed
platform tool (`removedPlatformTools`, with the replacement). The flag rules version goes to 4, so every workflow is
re-checked once and the break flags Workflow Review even when nothing changed. It is not a strict kind, so runs are
not blocked. `plan-drift-review.md` tells the review to drop the tool from the list and rewrite the instruction to use
the agent's own web search. Agents fix the workflows; nothing is edited by hand.

## Verification

GitHub verify run (`TestReferenceMapReportsRemovedTools`, existing reference map tests). After the next restart, the
next Workflow Review of an affected workflow should show the `removed_tool` break and remove it.
