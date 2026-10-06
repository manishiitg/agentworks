[← app / navigation](index.md)

# PLAT-607: Goals to Relays switch restores a Goal tab and returns to Goals

| Field | Value |
|---|---|
| State | deployed |
| Priority | P2 |
| Product | app |
| Area | navigation |
| Summary | Goals/Relays automatic chat restoration stays in the chosen product; deployed on Confida. |

## What happened

Rakesh on Confida reported that selecting Relays from Goals returned to Goals.
Goals and Relays share workflow mode and saved chat tabs. Product selection
cleared the active Goal preset, but App's restoration effect then considered
all workflow tabs. Activating a saved Goal tab selected its preset and Goals
again. A saved Relay tab did not help if a Goal Builder was more recent.
This is a separate tab-restoration path from the handler/mode fix in
[PLAT-563](plat-563.md).

## Fix

- Extract App's actual restoration effect into `useWorkflowTabRestore` so the
  production sidebar path can exercise it directly.
- Restore only tabs whose manifest belongs to the selected product and, when
  present, the selected preset. No fallback to another preset when its tabs
  are absent; do not infer Goals for an unknown preset before manifests load.
- Scope active and remembered tab checks to the same selection. A newly
  available manifest catalog retries restoration; stale hydration callbacks
  cannot reclaim a product the user left.
- Keep explicit workflow/tab navigation unchanged. Saved chats remain available
  when returning to their product.

## Verification

- The real React sidebar, stores and production restoration hook reproduced
  the return to Goals with and without a saved Relay chat. Both cases failed
  before the product filter and passed afterward, including repeated switches
  back to Goals restoring its saved chat. Only workspace file network reads
  are stubbed; this does not impersonate Rakesh's browser.
- 36 navigation and restoration tests across nine files passed. Targeted lint,
  TypeScript, full frontend build, release asset checks and bundle budget passed.

## Deployment

- Fix commit `3c33e6af3c6ad40780adb14cba9681ae05e4dd76` is on `origin/main`.
- Deployed 2026-10-06 to Confida as `confida-3c33e6af-20261006131824`,
  from shared build `3c33e6af-20261006111314`.
- Verified the live source revision and product filter, public index referencing
  the release asset, and public frontend JS hash matching the release file.
- Public health returned 200; deployed configuration checks passed. Full slot
  self-test: 77 passed, 0 failed, 35 skipped.

## Left

No implementation or Confida deployment work remains. Rakesh should reload
once to load the new frontend. His personal authenticated browser was not
available; behavioral verification used the real sidebar/restoration regression
above, and live verification checked the deployed assets and health.
