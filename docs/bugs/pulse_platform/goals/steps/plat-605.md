[← goals / steps](index.md)

# PLAT-605: Failed run record has no error or failed step

| Field | Value |
|---|---|
| State | open |
| Priority | P2 |
| Product | goals |
| Area | steps |
| Summary | A failed workflow run records status failed with no error or failed step; the cause is only in the server log |

## What happened

Upwork manual `daily-bid` run, 2026-10-06 10:48 to 10:51 UTC: `run_metadata.json` says `status: failed` with
`error` and `failed_step` empty. The cause was in the step's own `access_status.json` (`browser_failure:
browser_unavailable`: the Chrome extension timed out opening upwork.com after 1 minute) and in `server_debug.log`.

## Fix (not built)

Record the failing step and its error (or the step's own failure reason) in `run_metadata.json` and show it on the run.

