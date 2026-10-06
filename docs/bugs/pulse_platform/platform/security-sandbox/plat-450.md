[← platform / security-sandbox](index.md)

# PLAT-450 — Product owner backfill writes through cross-user symlinks

| Field | Value |
|---|---|
| State | deployed |
| Priority | P1 |
| Product | platform |
| Area | security-sandbox |
| Summary | fixed on `main`, not deployed. |

| Coordination | Value |
|---|---|
| State | fixed on main (2026-10-04), not deployed; regression tests added; the Crew move command follows the same rules (PLAT-442 step 4) |
| Priority | P1 |
| Date | 2026-10-04 |
| Owner | security-sandbox |

## Finding

PLAT-442 startup migration in `product_owner.go` reads and writes each
`product.json` as the app account, using `os.ReadFile` and `os.WriteFile`.
It verifies user/project directory entries but not the entire path's resolved
containment or the manifest's file type. Both calls follow symlinks.

## Reproduction and evidence

On reviewed AgentWorks `a04b393c9`, a temporary test created:

- `_users/zvictim/Chats/Work/projects/victim/product.json`, a valid existing
  Crew manifest lacking the new `owner_id` field.
- `_users/aattacker/Chats/Work/projects/bait/product.json`, a symlink to that
  victim manifest. The attacker's sorted name ensures its entry is read first.

Calling the production `migrateProductOwners` changed the victim manifest to
`owner_id: aattacker`. Report: scanned=2, stamped=1, current=1, mismatched=1,
no failures. The later victim scan merely logged the discrepancy and preserved
it. This used actual filesystem symlinks and migration code; no production
server, foreign real files or accounts were touched.

Condition: the target must be a Crew/Code manifest with no `owner_id`, as
expected during initial backfill. A target already stamped is not overwritten
by this particular probe. Source: `product_owner.go:162-190`, invoked from
`server.go` at startup. Manifest-first access can then consume the wrong owner.

## Left

Constrain the migration to the intended user's/project's real files, including
ancestor directories and the manifest. Reject symlinks and use safe anchored
opens/writes so a check/open race cannot redirect the mutation. Log skipped
unsafe paths. Add no-cross-user-write regression coverage and inspect backups
for mismatches before rollout; do not run a corrective migration on live files
without confirming the intended owner.

## Done (2026-10-04, not deployed)

- `migrateProductOwners` and `ensureProjectOwnerID` (`product_owner.go`) reach every project through anchored opens (`os.Root`): the docs root, `_users`, the
  user's directory and each folder of the project's path are checked with lstat to be real directories (never symlinks), each step opens a root that confines all
  later operations to that directory (a link that leads out is refused even if it is planted after the check), `product.json` must be a regular file, and the write
  is a new file in the project directory (created exclusively) renamed over `product.json`, so a symlink standing there is replaced, never written through.
  A symlink on the way is logged `[UNSAFE_PATH]`, counted (`unsafe=N` in `[OWNER_BACKFILL]`) and the project is skipped.
- The owner registry writer never writes through a symlink (temp file + rename, `O_NOFOLLOW` on its lock) and never rewrites an unreadable file.
- Regression tests with real temp files (`product_owner_test.go`): the attacker's symlink to the victim's manifest (victim's owner stays the victim's), a symlinked
  project folder, a symlinked `Chats` folder, a symlinked user directory, a manifest that is a directory; the lazy writer; `project_owner_registry_test.go`.
- The Crew move command must follow the same rules for every file it copies (see the PLAT-442 runbook): symlinks inside a Crew are copied as symlinks or refused,
  never followed.

## Left

- Inspect backups for manifests whose `owner_id` mismatches before any rollout that follows a PLAT-442 deploy; `owner_id` is information only now, so a stamped wrong
  value (from the old backfill) does not matter for ownership, but check what was stamped on the first deploy of `a04b393c9` if it ever ran.

## Register notes

[PLAT-450](plat-450.md), P1, fixed on `main`, not
deployed. The owner scan and the owner writers now use anchored, symlink-refusing
opens (`[UNSAFE_PATH]`); regression tests use real temp files and symlinks.
