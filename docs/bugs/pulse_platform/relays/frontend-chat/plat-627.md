[← relays / frontend-chat](index.md)

# PLAT-627: Update shared landing contract test for Python Relay guide

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | relays |
| Area | frontend-chat |
| Summary | The Python Relay landing props changed but a shared source assertion still expected the exact old JSX. |

## What happened

PLAT-611 (`67a79b014`) added `pythonRelay` to `WorkflowNewChatGuide`. The shared
`WorkflowResponsiveLayout` assertion expected the entire old JSX string. It
failed on main despite the correct guide rendering; focused Relay checks missed
this shared test. The assertion matches the parent source and fails after the
Relay change. This failure belongs to the Relay implementation.

## Fix

Check that the landing content uses `WorkflowNewChatGuide` and preserves
`relayMode`, allowing additional props. This retains the original test contract
without making every guide addition break the source assertion.

## Verification

- Reproduced before fix: responsive layout 1 failed / 2 passed.
- After fix: responsive layout + Relay source/intro/Python file checks: 4 files,
  7 tests passed.
- Current main backend checks passed, including real published Python starter,
  Relay/function/webhook regressions and product/livefeed checks.
- Full frontend audit: 504 files passed, 21 tests fail in four other files;
  Relay/layout checks pass. Other main failures tracked in
  [PLAT-626](../../app/tests/plat-626.md).

## Left

None for this regression. No product behavior changed.
