[← Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-382 — Browser toolbar and manual copy/paste

| Coordination | Value |
|---|---|
| State | implemented; locally verified; RTS deployment pending |
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

## Left

- Deploy with the normal RTS script; qualify Linux guarded browser paste.
- Follow-up: copying a selection inside a cross-origin frame (or closed shadow
  root) needs a scoped frame-aware reader. This version rejects it rather than
  copying another page or a shared OS clipboard. Plain-text paste still works.
- Clipboard images/files and Cut are outside this text copy/paste change.
