[← platform / browser](index.md)

# PLAT-382 — Browser toolbar and manual copy/paste

| Field | Value |
|---|---|
| State | deployed |
| Priority | - |
| Product | platform |
| Area | browser |
| Summary | deployed and verified on RTS (`f69f9fd-20261003144322`). |

| Coordination | Value |
|---|---|
| State | deployed and verified on RTS; cross-origin selection copy follow-up open |
| Date | 2026-10-03 |
| Owner | browser |

## Problem

The viewer repeated URL/title text and put tab operations next to the address
field. The user selected a neutral browser-like three-row layout. Copy/paste
into remote fields did not work after taking control; raw char events dropped
multiline text and native macOS Copy did not fire on an empty local input.

## Done

- Shared BrowserChrome header, tabs with close/+ controls, and navigation/address
  row. Existing neutral colors; no new colored buttons. Start hides when running.
- Native paste events and right-click Paste insert bounded chunks through the
  existing browser daemon's fixed keyboard insertText operation. Private service
  token, current workspace access and exclusive controller checks remain required.
- Copy reads only the active page selection, including open shadow roots and
  same-origin frames, then writes the viewer's local clipboard in the original
  user gesture. Empty selection does not overwrite it. A harmless local selection
  supports the native macOS Copy menu. No server OS clipboard or text persistence.
- Native Cmd/Ctrl shortcuts follow the remote platform. Watchers cannot navigate,
  paste or copy. Context actions disappear on control/session change.
- Local full workspace suite, browser WS authorization/proxy checks, focused UI
  tests, frontend release build and real Chrome multiline Unicode paste/selection
  tests pass. Native WKWebView Command-C and paste-event checks pass.
- Design comparison and interaction evidence are in project-root design-qa.md.
- Source commit `992ee1c41d53b62b394498f4a87b6a8c0d02c484` is on main.
  Normal `DEPLOY_SLACK_NOTIFY=0 ./deploy.sh rts` deployed release
  `f69f9fd-20261003144322` (builder source `f69f9fd00797923795ccfd5ff92d41be9b725840`,
  containing the implementation). Agent, workspace and gateway are active;
  both health endpoints report healthy. The public browser JS chunk includes
  the new toolbar and clipboard protocol.
- On RTS Linux, `TestBrowserRealGuardedStartup` passed against real guarded
  headless Chrome, preserving multiline Unicode, quotes and literal shell-like
  text through viewer IPC. `TestBrowserViewerTextUsesOnlyExistingSessionIPC`
  also passed. The temporary browser/profile/clone were removed by the fixture.
  Native clipboard UI was verified locally; no live user website was changed.

## Left

- Follow-up: copying a selection inside a cross-origin frame (or closed shadow
  root) needs a scoped frame-aware reader. This version rejects it rather than
  copying another page or a shared OS clipboard. Plain-text paste still works.
- Clipboard images/files and Cut are outside this text copy/paste change.

## Register notes

[PLAT-382](plat-382.md), deployed and verified on RTS
(`f69f9fd-20261003144322`). Neutral three-row browser controls, native multiline
paste and selection copy behind exclusive manual control; real guarded Linux
paste passed. Cross-origin-frame selection copy remains a tracked follow-up.
