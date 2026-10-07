[← coding-agents / accounts](index.md)

# PLAT-688: Muse Check usage fails for non-managers

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | coding-agents |
| Area | accounts |
| Summary | Check usage on a shared account showed terminal-query garbage and 'cursor position could not be read' instead of Muse's usage, for people who do not manage the account |

## What happened

## Fix

## Left

## Report

#agent_works (Ashutosh, Excellence, Crew → Models → Check usage; first 2026-10-05, "still the same" 2026-10-07 16:13): the box shows `10;?␇11;?␇4;0;?␇4;1;?␇…` and `cursor position could not be read within a normal duration`.

## Cause

For someone who does not manage the account, the server runs the usage command in a read-only session and returns its text. Muse is a full-screen CLI: it asks its terminal for the cursor position (DSR), device attributes and colours (OSC 10/11/4) before drawing. In a browser terminal xterm answers; in the read-only run nothing did, so Muse gave up. The text cleaner also matched only `ESC ]` of the OSC queries and left their bodies.

## Fix

A read-only usage session answers those queries itself (cursor at 1;1, VT220 attributes, white/black/grey colours), including ones printed before it was marked read-only, and the cleaner removes whole OSC sequences and BEL. Test: `TestReadOnlyUsageAnswersAndStripsTerminalQueries`.
