[← browser / browser](index.md)

# PLAT-643: Show the extension version and update status in the browser panel

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | browser |
| Area | browser |
| Summary | The browser panel shows the connected Browser Bridge version and whether an update is available |

## Why

Owner, 2026-10-07: after PLAT-636 shipped Browser Bridge 0.4.8, the local Chrome still ran 0.4.6 from an unpacked
folder in Downloads, and nothing in the app said so.

## What changed

- The relay records the version the connected extension reports in its diagnostics (`connection_paired` is sent
  right after pairing). Only a short dotted number is accepted. A new connection starts with no version.
- The server reads the latest version from the `manifest.json` in the extension zip it ships for download.
- `GET /api/browser/extension` returns `extension_version` and `latest_extension_version`.
- The browser panel's status card shows "Browser Bridge 0.4.8 · up to date", or an amber "Browser Bridge 0.4.6 ·
  update to 0.4.8" with how to update and a download button. An extension too old to report a version shows
  "version unknown".

## Verification

Server builds; browserrelay tests pass; the server reads 0.4.8 from the bundled zip. Frontend type check clean;
workflow component tests pass apart from 3 failures already on main (WorkspacePanelGuideButton x2,
workspaceToolbarPlacement). Not yet seen in the running app: check the panel after the next local restart.
