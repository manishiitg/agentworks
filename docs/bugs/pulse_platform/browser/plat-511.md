# PLAT-511 — Recording stale-state test lacks the native browser IPC fixture

State: open. Date: 2026-10-05. Priority: P3. Area: browser test harness.

## Evidence

`go -C workspace test ./handlers -count=1` fails only
`TestUserCaptureStaleStateStartsFreshAndRejectsBlankEvidence` at
`browser_capture_stream_test.go:158`: starting capture returns
`Recording could not start: browser IPC unavailable`.

The test mocks the agent-browser executable and writes a stream marker, but does
not supply the native browser IPC expected by the current recording path. The
same focused test fails with the unchanged `browser_session_tracker.go` restored
from the worktree's starting commit `f1326779b`; the Chrome extension change is
not required to reproduce it. Other browser/debug-log handler checks pass.
No user-visible recording failure has been established by this fixture result.

## Remaining

Update this stale-state qualification to exercise the actual native browser IPC
or a real isolated browser recording path, retaining its stale-state and blank
visual-evidence assertions. Rerun the complete handler suite. No recording
implementation or fixture change is bundled with PLAT-510.
