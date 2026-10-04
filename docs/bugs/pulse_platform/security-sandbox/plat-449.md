[← Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-449 — Editable product owner selects another user's CLI identity

| Coordination | Value |
|---|---|
| State | fixed on main (2026-10-04), not deployed; regression tests added; the `Crew/<id>` half is covered by the Crew move (PLAT-442 step 4) |
| Priority | P1 |
| Date | 2026-10-04 |
| Owner | security-sandbox |

## Finding

Review of AgentWorks `a04b393c9`, especially PLAT-442 commits `9901c8b94`
and `5dad1a36c`, with provider `ae8e204` and mcpagent `ffc32d7`.

`product.json` is user-editable project data, but `resolveProjectOwner` trusts
its `owner_id` ahead of the physical owner, logs disagreements and still
returns that identity. `decideTurnRunAs` uses it to select a Linux slot.
Meanwhile `resolveCrewProjectBinding` admits the caller as owner based on their
own project tree. These authorities can disagree.

## Reproduction and evidence

An isolated temporary test used `newMultiUserFixture` and the actual proxy,
project binding, CLI working-directory and run-as decision helpers:

1. User A's PUT of their Code's `product.json` passes the proxy (status 0).
2. Change only `owner_id` to B, retaining every other manifest field.
3. A still resolves the project with `OwnedByCaller=true`.
4. `decideTurnRunAs` returns B and B's `slot09`, despite A being the caller
   and the project still being under A's private tree.

The probe passed, logging `[OWNER_MISMATCH]` followed by the wrong launch
identity. No real CLI was launched and no live account/files were touched;
this confirms wrong identity selection, not a demonstrated Linux exploit.
PLAT-451 records a separate tmux fallback when that script and folder disagree.

Sources: `agent_go/cmd/server/product_owner.go:203`, `run_as.go:120`,
`crew_access.go:145`, `workspace_proxy_policy.go:104` (server-owned protections
exclude project owner metadata).

## Left

Make ownership authoritative server-controlled metadata rather than accepting
an arbitrary user-writable `owner_id` for an OS identity. An authenticated
transfer, if supported, must validate and update all authorities together.
For private Code, reject mismatches with its admitted owner before launch.
Cover both browser/proxy writes and native edits; protecting only the UI is
insufficient. Add regression coverage before deploying PLAT-442.

## Done (2026-10-04, not deployed)

- **Server-controlled owner registry** (`project_owner_registry.go`): `<state root>/ownership/projects.json`, directory 0700, file 0600, app-owned, outside the docs
  root (so neither the workspace proxy nor a CLI folder guard can reach it), keyed by product and folder name, atomic temp-file + rename writes serialized by an
  exclusive file lock (the server and the migration command both write), reads refresh when another process changed the file, never rewrites an unreadable file,
  never changes a registered owner (`errProjectOwnerConflict`), never writes through a symlink. A test binary never touches the real state area.
- **Resolution** (`resolveProjectOwner`): the registry entry; else the owner the PHYSICAL PATH names; else (a project at `Crew/` with no entry) nobody. `product.json`
  is never read for ownership; its `owner_id` is still written, as information. `resolveCrewPath`, `readCrewManifestOwner` (the shared-root owner) and
  `decideTurnRunAs` use it.
- **Writers**: the startup scan (`migrateProductOwners`), the owner opening a project (`ensureProjectOwnerID`) and server-side Crew creation
  (`writeCrewCreationManifests`) register the owner (from the path / the creating user); the Crew move registers what it moves.
- **Admission**: a project found in the caller's own tree that is registered to someone else (a copy or planted folder) is refused (`resolveCrewProjectBinding`,
  `[OWNER_MISMATCH]`). A private Code launch whose resolved owner is not the admitted caller is refused BEFORE any CLI starts, with an explicit error
  (`checkProjectLaunchOwner`, `errProjectOwnerMismatch`), no fallback.
- **Tests first/with**: `multiuser_identity_test.go` `TestEditedManifestOwnerNeverChangesOwnershipOrLaunchIdentity` (A edits their Code's owner_id to B through the proxy
  gate and by a native edit; A still opens it, the launch identity stays A's, B's slot is never selected, B cannot open it; B plants a copy of A's Code in B's tree:
  refused, no launch; the same for a Crew in its owner's tree and for a Crew at `Crew/<id>`), `product_owner_test.go`, `project_owner_registry_test.go`.

## Left

- Ownership transfer is not supported and must not be added without updating the registry and the project's location together (DECISIONS 2026-10-04).
- UI-created Crews and Codes get a registry entry on first open or the next start (path owner); until then the path is the authority, which is the same owner.
- Review the registry after the first deploy: `[OWNER_BACKFILL] ... conflicts=N` and `[OWNER_MISMATCH]` lines list folders registered to one owner that also exist in
  another tree; do not "fix" them on live data without confirming the intended owner.
