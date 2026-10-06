[← browser / browser](index.md)

# PLAT-586: Project-name lookups block the extension heartbeat message loop

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | browser |
| Area | browser |
| Summary | Cache display names and resolve them on a bounded background worker, outside the websocket reader. |

## What happened

6276ba934 (PLAT-573) reread all project manifests sequentially on every
25-second heartbeat. Each lookup could wait one second while CDP replies queued
behind it in the same reader.

## Fix

Each connection reads only its selected project name before entering the
reader. A single background worker resolves other/new grant tuples and refreshes
cached names every five minutes; heartbeat handling only copies cached values.
Removed grant entries are pruned and stale lookup completions cannot populate a
replacement entry. Pending names fall back to the folder basename. Renames
appear after refresh and the next heartbeat, or immediately on reconnect.
Live authorization checks remain on every heartbeat, independently of metadata.

The live websocket regression deliberately blocks a secondary manifest lookup:
pairing, repeated pings and a CDP reply still complete, and unchanged heartbeats
perform no further display-name reads. Existing ownership/revocation coverage
and real Chrome Code/Crew/workflow group checks also pass.

## Left

Deploy the backend/workspace change to RTS. No extension reinstall is required for this fix.
