[← browser / browser](index.md)

# PLAT-587: Record the selected shared Chrome extension tab to a guarded workspace video

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | browser |
| Area | browser |
| Summary | Extension 0.4.4 records the existing shared tab to a guarded WebM/MP4 and fails interrupted takes. |

## What happened

The extension adapter rejected record; the requested successful recording
E2E therefore needed feature support, explicitly approved by the owner.
The current agent-browser 0.38.2 recorder captures the existing page via CDP.
Its first run exposed unsupported page-session Target.getTargetInfo; the next
run exposed a reused session ID that let the recorder consume navigation events,
so open timed out while recording.

## Fix

Allow record start <workspace-path.webm|.mp4> [url] [--fps 1-60] and record stop.
Validate options before mutation; reject restart, overlays and extra artifacts.
The existing artifact broker stores an owner/connection lease, stages in the
private browser folder and publishes the exact source under current write grants.
Tab changes/creation/close and foreign inline targets are blocked during a take.
Stop operates on the lease even if the recorded target is no longer available.

The extension resolves page-session target metadata only for that granted root.
Explicit attaches receive distinct logical flattened sessions on the same
physical debugger. Lifecycle events fan out to automation/recorder; screencast
frames go only to their capture session. Detaching capture never detaches the
physical shared tab. Unsharing/debugger loss interrupts a take; recovery does
not restart recording. The relay counts adapter-originated capture interruption events. Stop first
finishes the encoder with transfer-only staging; the backend compares that take's
interruption epoch before a separate trusted finalization, because the CLI treats
Inspector.detached as graceful success. Interrupted and definitive CLI stop
failures release the failed lease
without publishing footage. CDP client disconnect clears its logical sessions
and stops their screencast. No browser permissions were added. Both runtime Docker images now include ffmpeg.

The real Chrome 154 E2E runs record start → navigate → fill/click → record stop,
checks no new tab/focus change, rejects tab switching during capture, checks a
nonempty WebM header and decodes the file with ffmpeg. A second take loses its
debugger deliberately: stop fails, no video is published, and a fresh take works.
Screenshots and the existing authority/reconnect/group checks also run.

## Left

Deploy the backend/workspace change to RTS. Install/reload extension 0.4.4; the workspace needs agent-browser 0.38.2 and ffmpeg with libvpx/libx264. There is no microphone or desktop audio; stop before disconnecting. Bundled capture/HAR, cursor/contact sheets and restart are not supported on this connection.
