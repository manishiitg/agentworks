# PLAT-511 — Recording stale-state test lacks the native browser IPC fixture

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P3 |
| Product | browser |
| Area | browser |
| Summary | Native Unix IPC fixture restores stale-state and blank-footage qualification; the handler suite passes. |

State: fixed on main. Date: 2026-10-05. Priority: P3. Area: browser test harness.

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

## Fix

Replace the fake executable with a real Unix socket speaking the native daemon
request/response protocol, including matching response IDs and HAR export.
The test retains the real websocket frame stream and ffmpeg encoder, stale
capture reconciliation, fresh output directories, blank-footage rejection and
missing-browser cleanup. It asserts capture commands traverse the IPC fixture.
This is a protocol fixture, not a claim that a real Chrome supplied the blank
frames. Real shared-page Chrome recording is qualified separately by PLAT-587.

Verification: the complete workspace handler suite passes on macOS, with ffmpeg
available and this test actually executed.

## Remaining

None for this fixture regression. Production recording implementation is unchanged.
