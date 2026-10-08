[← coding-agents / accounts](index.md)

# PLAT-716: Check usage times out after 30 s in the browser

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | coding-agents |
| Area | accounts |
| Summary | Check usage on a shared Codex account always failed in the browser with 'timeout of 30000ms exceeded' |

## What happened

## Fix

## Left

## Seen

Excellence, 2026-10-08 09:15, 09:17, 09:20 CEST: Ashutosh pressed Check usage on the shared Codex account three times; each showed "timeout of 30000ms exceeded". The server log has `[PROVIDER_SETUP] codex-cli usage for server account` for each, and no failure on the server side.

## Cause

The browser's provider API client has a 30 s timeout for every request. The usage request starts the CLI and then waits up to 45 s for its answer (`providerUsageCollectTimeout`), and Codex takes more than 30 s to start and answer `/status`.

## Fix

`checkProviderUsage` uses a 90 s timeout for this one request.
