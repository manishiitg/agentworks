[← Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-529 — `formsKitAdoption.test.ts` "builds folders and browser settings from the kit" fails on main

| Coordination | Value |
|---|---|
| State | open: found 2026-10-05 while testing PLAT-528; not caused by it |
| Priority | P3 |
| Date | 2026-10-05 |
| Owner | frontend-chat |

## Evidence

`npx vitest run src/components/workflow/formsKitAdoption.test.ts` on a clean `origin/main` worktree: 1 failed, 8 passed; `AssertionError: expected 1 to be +0`. The rest of `src/components` and `src/products` (1691 tests) passes.

## Left

Find which settings-form change made the test (or the form) drift and fix whichever is stale; check CI is red on main for the same reason (PLAT-489 style stale test fixes).
