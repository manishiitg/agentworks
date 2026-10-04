[← Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-450 — Product owner backfill writes through cross-user symlinks

| Coordination | Value |
|---|---|
| State | open; reproduced with real temporary files |
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
