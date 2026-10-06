# PLAT-393 — Browser teaching restart, compact chrome and tab restoration

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | browser |
| Area | browser |
| Summary | fixed on main, deployed to RTS and verified in `7e2ea79-20261003164344`. |

- State: fixed on main, deployed to RTS and verified.
- Priority: P1 for teaching/browser loss, P2 for browser chrome and tab memory.
- Reported: 2026-10-03, RTS Code and local screenshots.

## Problem and cause

The browser used three tall header rows, forgot tabs after a managed restart,
and failed to reconnect after starting teaching on RTS Code.

RTS Code's stream disappeared at 15:52:34 UTC as teaching returned 503. An owned
fixture reproduced the destructive attachment: guarded Chrome was started with
the system CLI; teaching resolved the service account's other CLI, emitted
`Daemon version mismatch detected, restarting...`, and restarted the daemon.
Combining stderr with stdout then broke CDP endpoint JSON decoding. RTS had
0.38.2 in the service prefix, 0.37.0 in /usr/local and 0.34.0 in /usr/bin.
Viewer reconnect only retried WebSocket connections and could not revive a dead
browser; manual Start also did not reset an exhausted same-session retry budget.

## Change

- Two 36 px header rows: browser/tabs/neutral actions, then navigation/address/status.
  Narrow panels use accessible icons and horizontal tab scrolling.
- Teaching discovery, tab inspection, reviewed replay and managed diagnostic
  capture use finite operations
  over the existing daemon's IPC, with no launcher or version upgrade involved.
- Remember up to 50 safe HTTP(S)/blank URLs and active tab in profile-owned 0600
  storage. Snapshot once per second while the daemon remains alive, including
  after its viewer closes. PID changes stop old snapshots before a fresh blank
  browser can overwrite memory. Reopen blank startup or reuse Chrome-restored
  pages; different existing pages and repeated Start are preserved.
- One authorized managed-browser recovery after reconnect fails while the viewer
  previously had control. No passive/Playwright launch or physical CDP restart;
  interrupted teaching remains stopped. Manual Start retries its connection.
- Keep deployment and behavioral guidance in the consolidated browser guide.

## Verification

Passed: 34 focused frontend teaching/lifecycle/control/recovery tests; full workspace Go
suite; focused agent-server auth/lease/restore bridge tests; native real-browser
teaching/replay and guarded restart/tab/sign-in/paste fixture; real RTS guarded
teaching/replay with unchanged daemon PID, multiline paste, duplicate tab URLs,
closed-tab removal, active-page restoration and retained sign-in. Browser-rendered
QA at 1017 px and 420 px confirms exactly two 36 px rows without outer overflow.
Full frontend production build, catalog, release-asset and bundle-budget checks
pass. Deployed with `DEPLOY_SLACK_NOTIFY=0 ./deploy.sh rts` to release
`7e2ea79-20261003164344` from commit
`7e2ea793742ed21c57fe71df9a403eea030c3210`. Verified the active source manifest,
all three services active, healthy agent/workspace endpoints, public HTTP 200,
same-origin runtime configuration and compact browser CSS in the served release.

## Boundaries

Tab memory stores URLs and selection, not unsaved form contents or DOM state.
A sudden crash can lose changes since the last one-second snapshot. It starts
when a managed browser is started from the UI or its live viewer opens; unviewed background-only browsers do not yet
create this snapshot. Local CDP retains Chrome's own restoration. The original
lost RTS tab strip predates these snapshots and cannot be reconstructed by this
change. General recorder coverage limits remain in `docs/core/browser.md`.

## Register notes

[PLAT-393](plat-393.md), fixed on main, deployed to RTS
and verified in `7e2ea79-20261003164344`. Two compact header rows, profile-owned tab memory, direct teaching IPC
and bounded recovery of a previously controlled managed browser.
