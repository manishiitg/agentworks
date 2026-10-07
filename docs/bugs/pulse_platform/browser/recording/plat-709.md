[← browser / recording](index.md)

# PLAT-709: Saved recordings unreadable by the slot user who made them

| Field | Value |
|---|---|
| State | deployed |
| Priority | P1 |
| Product | browser |
| Area | recording |
| Summary | Browser recordings and screenshots saved into a slot user's project were 0600 for the server account, so the user's own chat got Permission denied |

## What happened

## Fix

## Left

## Seen

RTS, 2026-10-07: an SDE Code project (slot01) recorded `recordings/session-video-3.webm` with the Browser Bridge. The file was `video-studio:slot01 0600` and the new `recordings/` folder `2750`, so the chat (running as slot01) got "Permission denied" reading or copying it and could not attach it.

## Cause

`security.FinalizeBrowserArtifact` (workspace server, service account) copies the staged file through `os.CreateTemp`, which creates 0600, then renames it into place; missing folders were made with `MkdirAll(…, 0755)` under the service umask.

## Fix

- The published file gets the read/write access its folder gives (2770 project → 0660).
- Missing folders are created with the nearest existing folder's mode, including setgid.
- Test: `TestFinalizeBrowserArtifactKeepsTheProjectGroupsAccess`.

## Deployed

RTS, Excellence and Confida on 9d92e37 (2026-10-07). The four files saved before the fix on RTS were set to 660 (folder 2770) by hand; the owner confirmed the chat could then attach the video (2026-10-08).

## Left

Not checked live in a Crew or workflow folder (the fix copies the folder's own access, read from the code).
