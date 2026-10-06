[← app / workspaces](index.md)

# PLAT-597: Workflow deletion partially removes workspace before sandbox permission failure

| Field | Value |
|---|---|
| State | deployed |
| Priority | P2 |
| Product | app |
| Area | workspaces |
| Summary | Atomic workspace deletion and accurate cleanup warnings are deployed and verified on Excellence and Confida. |

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

No remaining implementation/deployment work for the reported failure.
Historical partially deleted folders are not automatically removed or recovered
by this fix. Existing user files were not touched during testing.

## Deployed verification — 2026-10-06

Source commit `ebd8c2e28f445ba9aa55b5b36fab61c882634f90`; shared build
`ebd8c2e2-20261006101726`.

- Excellence: `agents-ebd8c2e2-20261006122226`.
- Confida: `confida-ebd8c2e2-20261006122503`.
- Both guarded deployments completed; running configuration checks and public
  `/api/health` passed (200), including the full slot checks.
- Both actual authenticated `/api/workflows/folder` APIs were exercised with
  unique disposable workflows explicitly owned by an active administrator,
  containing slot-owned 0700 private fixture directories. Both returned 200,
  `success=true`, `cleanup_pending=true`, and the administrator-cleanup message;
  original workspace paths were absent, remnants were isolated outside docs in
  service-owned 0700 staging. Fixture remnants were removed afterward by the
  operator. No bearer tokens or fixture credentials were printed or persisted.
