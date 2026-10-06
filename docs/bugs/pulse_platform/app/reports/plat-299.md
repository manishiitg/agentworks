[← app / reports](index.md)

# PLAT-299 — Authenticated streaming video/audio evidence in workflow reports

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | app |
| Area | reports |
| Summary | adds `window.report.mediaUrl`, file-scoped expiring playback credentials, authenticated range streaming, and report guidance for durable test recordings. |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | Implemented locally — targeted tests pass; not deployed |
| Last synchronized | 2026-09-07 |
| Priority | P1 reporting usability |

## Problem

Browser test recordings can be saved under `db/assets/`, but report `fileUrl`
downloads the complete file to a blob before playback. Large recordings need
streaming and seeking. The workspace raw-file handler already uses ServeContent;
reuse that implementation instead of introducing separate storage.

## Contract and implementation

- `window.report.mediaUrl('db/assets/test-videos/<run>/<test>.webm')` returns a
  short-lived playback URL usable by native video/audio controls. It is wired
  into the shared report runtime, early-call bootstrap and headless preview.
- POST `/api/workflow/report-preview/media-url` accepts authenticated
  `{workspace, path}`; report-preview credentials remain bound to their workflow.
- GET/HEAD `/api/workflow/report-media?token=...` streams one exact file. JWT
  credentials bind user, workflow, path and expiry, grant no other API access,
  expire within 30 minutes and cannot outlive the issuing credential. Never put
  the general account token into a video URL.
- Current workflow access and disabled-user checks apply to playback requests.
  The proxy passes user identity and service authentication to workspace.
- Restrict paths to durable `db/assets/` media, reject traversal and HTML, and
  prevent symlink escapes from the asset directory. Support MP4/WebM and common
  audio formats; browser codec support still determines actual playability.
- Preserve Range, If-Range, Content-Range, Accept-Ranges and relevant conditional
  headers. Preserve 206/416 responses and use streaming io.Copy, not full buffering.
- Keep `fileUrl` unchanged for existing report consumers. No new storage service.

## Authoring and test evidence

Save the completed recording to a unique durable path, store that path with its
run/test result, and display a Watch recording action. Request a fresh URL on
opening the player. Do not store expiring URLs in the database. Expired playback
should offer a refresh/retry; application authors can preserve the current time.
Guidance includes a native-player example and visible error handling requirements.
Automatic test recording, retention policies and an automatic video-results widget
are outside this implementation; existing workflow recording configuration still
controls capture. Related: [PLAT-298](../../goals/steps/scripted/plat-298.md)), scripted execution reliability.

## Verification

- [x] Media scope cannot access other API routes; malformed paths are rejected.
- [x] Range requests return exact requested bytes, correct headers and 206.
- [x] Unsatisfiable ranges preserve 416; HEAD omits the body.
- [x] Symlink escape from durable assets is denied.
- [x] Minted credentials bind file/workflow/user, respect parent expiry, and expired tokens fail authentication. Existing workflow visibility checks run on issuance and playback.
- [x] Report bootstrap tests and TypeScript build pass; preview API is wired into the shared runtime.
- [ ] Live multi-user access-revocation and long-video browser seek checks after deployment.
- [ ] Deployed browser playback verified (pending deployment).

## Deployment

Ship agent backend, workspace backend, frontend and report-preview bundle together.
No production deployment has been performed for this request.

Local verification: `go test ./cmd/server -run 'TestReportMedia|TestReportPreview'`,
workspace `go test ./handlers -run TestReportMedia`, frontend `tsc -b`, and the
report bootstrap Vitest suite passed on 2026-09-07.

## Register notes

[PLAT-299](plat-299.md) adds `window.report.mediaUrl`, file-scoped
expiring playback credentials, authenticated range streaming, and report guidance
for durable test recordings. Implemented locally; targeted tests pass and
deployment pending. Existing `fileUrl` behavior remains compatible.
