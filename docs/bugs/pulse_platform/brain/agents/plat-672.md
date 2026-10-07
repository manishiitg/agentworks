[← brain / agents](index.md)

# PLAT-672: Brain: say which folder is missing

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | brain |
| Area | agents |
| Summary | create_folder under a missing parent returned only 'Resource not found.', so a workflow step kept retrying |

## What happened

## Fix

## Left

## What happened

RTS, 2026-10-07: the rtslatency weekly knowledge refresh (re-run to verify PLAT-651; its 16 Brain reads and writes now work) called `create_folder` under `RTS/Engineering` several times. `RTS/Engineering` does not exist, and every call returned `NOT_FOUND: Resource not found.`

## Fix

When a folder path does not exist and the caller can read the nearest existing folder above it, the error names the missing folder and the create_folder call that makes it. Otherwise it stays the uniform `Resource not found.` (no existence leak for folders the caller cannot see).
