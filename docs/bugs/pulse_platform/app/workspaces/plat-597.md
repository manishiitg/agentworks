[← app / workspaces](index.md)

# PLAT-597: Workflow deletion partially removes workspace before sandbox permission failure

| Field | Value |
|---|---|
| State | in progress |
| Priority | P2 |
| Product | app |
| Area | workspaces |
| Summary | Folder deletion now removes the workspace atomically and reports any private sandbox files awaiting administrator cleanup; verified locally and through a real Linux service, deployment pending. |

## What happened

2026-10-06 report: deleting "Timouthy - Candidate Sourcing Pipeline" displays
"please try again", but a hard refresh removes it from the list. Owner also
identifies Excellence as affected. The supplied browser-extension GET is a
follow-on request for the deleted workspace, not the delete request.

Confida logs prove `DELETE /api/workflows/folder` for
`Workflow/briancandidatesourcingpipeline` returned 500 at 15:29:34 IST;
the workspace service's recursive delete returned 500 too. Later retries
returned 403 because the manifest was already removed. Remaining files include
slot `cf09`'s 0700 GWS configuration directories below `.sandbox-cache`.
The app service cannot traverse them. Recursive removal deleted ordinary files,
including workflow.json, before encountering the permission error.

## Fix

- Move the complete folder with one rename into a fresh service-owned 0700
  directory beside the documents root, outside document serving and sandbox
  grants; cleanup happens only after the original workspace path disappears.
- Staging/rename failures return an error without partially removing files.
  Cross-filesystem rename fails safely; no fallback to destructive live removal.
- A cleanup permission error returns success plus `cleanup_pending`, logs the
  operator-only cleanup path, and does not widen any slot permissions.
- The workflow API propagates that flag and performs normal runtime/journal
  cleanup; both workflow deletion UI paths show success or the explicit
  administrator-cleanup warning.
- Existing owner authorization and confirmation remain required.

Verification:
- Focused DeleteFolder handler tests pass, including the original per-user path
  and confirmation regressions and a real permission-denied-directory case.
- Existing agent workspace proxy policy checks pass.
- Full frontend production build passes, including TypeScript, release asset
  and bundle budget checks.
- Candidate Linux workspace server on private loopback port 25994, running as
  `confida`: an ordinary folder is completely removed; a slot-owned 0700
  directory returns 200 with cleanup_pending, original workspace absent, and
  remaining fixture isolated in service-owned 0700 staging outside docs.
  The candidate and its temporary fixture were removed afterward.

## Left

- Deploy to Excellence and Confida and verify the actual workflow API with a
  disposable, explicitly owned regression workspace.
- Historical partially deleted folders are not automatically removed or
  recovered by this fix. Existing user files were not touched during testing.
