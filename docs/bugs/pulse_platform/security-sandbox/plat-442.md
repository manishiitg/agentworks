# PLAT-442 — Identity is explicit: owner from the manifest, slot named by the platform, Crews at `Crew/<id>`

Status: in progress (2026-10-04). Step 1 on main, not deployed; steps 2, 3, 5 below as they land. Step 4 (the Crew move) is not started. Follows PLAT-435 (one path type in agent_go).

## Problem

Who owns a project and which Linux slot a CLI runs as are both read out of the folder path (`_users/<id>/...`), in three separate copies of the rule:
agent_go (now `pkg/workspaceref`), the `workspace/` module (`slots.SlotForDir`, `slots.IsCodeProjectDir`, `utils/path.go`), and the provider
(`internal/slotfs.SlotOf`). Consequences found 2026-10-04:
- A Crew Run-mode reader's turn runs in the owner's folder, so it runs as the **owner's slot**. That is the owner's choice (below), but it was implicit.
- Goals (`Workflow/<name>`) have no `_users/` segment, so they run as the app account. Also the owner's choice (below), also implicit.
- Moving Crews to a shared `Crew/<id>` (crew_shared_root.md) would silently drop their slot isolation: the path would no longer name an owner.

## Decisions (owner, 2026-10-04)

1. Crew reader turns run as the **owner's slot**; the reader block, tools and folder guards limit the reader.
2. Goals keep running as the **app account** with Landlock and per-workflow folders; no slot per workflow.
3. Crews **move to `Crew/<id>`** in this round, after steps 1-3, with backup, dry run and old paths kept as aliases; Confida first.

## Plan

1. **Owner from the manifest.** Workflows (`workflow.json` owners), Crews and Codes (`product.json` gains `owner_id`, backfilled from today's path).
2. **The platform names the slot.** Each launch passes the run-as slot explicitly (Code and Crew: the owner's slot; Goals: none). `slotfs.SlotOf` and
   `workspace/slots` stop inferring it from paths; a launch without an explicit slot runs as the app account.
3. **One path library.** The `workspace/` module uses `workspaceref` (shared module or vendored copy); guard test over both modules.
4. **Crew move** to `Crew/<slug>-<id8>`: migration command with dry run and backup, aliases for both old spellings, per-server rollout.
5. **Multi-user test fixture:** two users; owner and Run-mode reader of a Crew; Code privacy; Goals; the OS identity of each launch checked.

## Done

### Step 1: owner from the manifest (2026-10-04, not deployed)

Where each product's owner lived before: Workflows in `workflow.json` (several owners); Crews and Codes only in the folder path
(`_users/<owner>/Chats/{Work,Code}/projects/<id>`), except a `Crew/<id>` root, whose `crewOwners` cache already read `owner_id` from
`product.json` (`crew_ref.go`) although nothing wrote it.
- `product.json` of a Crew gets `owner_id` at server-side creation (`writeCrewCreationManifests`, so `create_crew` and import). Crews and Codes
  created by the UI write `product.json` from the browser without it; the backfill covers them.
- Backfill (`product_owner.go`), idempotent, never overwrites an existing `owner_id`, moves no data: (a) a startup scan
  `migrateProductOwners` over `_users/*/Chats/{Work,Code}/projects/*/product.json` (log `[OWNER_BACKFILL] scanned=... stamped=...`), (b) lazily when
  the owner opens a project (`resolveCrewProjectBinding`).
- Reads: `resolveCrewPath` (every crew API edge) takes the manifest's `owner_id` and falls back to the path owner; a disagreement logs
  `[OWNER_MISMATCH]` and uses the manifest, it never fails a request. `productProjectManifest` carries `owner_id` so rewrites keep it.
- Found while doing it: `readCrewManifestOwner` turned a manifest WITHOUT `owner_id` into the owner `default` (`sanitizeUserIDForPath("")`), so any
  `Crew/<id>` manifest lacking the field was "owned" by a user called `default`. Fixed (`cleanManifestOwner`: empty stays empty).
- Tests (`product_owner_test.go`): stamp never overwrites and keeps other fields; the pick rule; startup scan (stamps, idempotent, leaves a
  disagreeing and a foreign manifest alone); manifest vs path agree for EVERY physical project root a fixture in `cmd/server/*_test.go` spells
  (scanned by regexp); `resolveCrewPath` prefers the manifest.

### Step 3: one path library (2026-10-04, not deployed)

- **Shared how.** `agent_go` already imports the `workspace` module (`replace ... => ../workspace`) and the workspace module cannot import agent_go,
  so the package moved INTO the workspace module: `workspace/workspaceref` (code and its table tests moved with `git mv`). `agent_go/pkg/workspaceref`
  stays as a thin re-export (type alias for `Ref`, the constants and the six functions), so none of the ~60 agent_go files that import it changed (other
  sessions are editing them). No import cycle, no new module: the deploy builds (`deploy/rootless-linux/build-and-activate.sh`: `go build
  .../workspace` and `.../agent_go/cmd/agentworks` with the same `replace`) are unchanged. Verified `GOWORK=off go build ./...` of the workspace module
  (also `GOOS=linux`) and the agent_go packages that do not use the new provider API.
- **Migrated** (all through `workspaceref.Parse`): `utils.UsersDirectory` (now the shared constant), `utils.userTreeOwner`,
  `utils.ConvertToUserRelativePath`, `utils.ResolveUserPath` (physical path via `PhysicalPathOf`; its user sanitizer stays the workspace one, see
  PLAT-440), `handlers/query.go normalizeOwnedPerUserDBPath`, `handlers/documents.go` root-listing filter, `server.go` default folders,
  `skill_sync.go` project skills-folder shape (`isProjectSkillsDir`), `slots.IsCodeProjectDir`, `slots.ExecConfig.SlotForDir`.
- **Guard.** `pkg/workspaceref/guard_test.go` now walks agent_go AND `../workspace` (own allowlist: `utils/path.go` only, for the constant); a probe file
  with a `"_users/x"` literal in the workspace module fails it.
- **Tests (both spellings, another user refused):** `utils/path_spellings_test.go`, `handlers/db_path_spellings_test.go` (own logical, own physical,
  own physical absolute, another user's physical and absolute physical, the users folder), `skill_target_spellings_test.go`,
  `slots/identity_table_test.go` (regression table of `SlotForDir` and `IsCodeProjectDir`, run against the unmigrated code too: same results).
- **Behaviour changes, where the old code was wrong on a spelling:** `skill_sync` accepted `_users/./Chats/Code/projects/p/skills` (owner `.`),
  which resolved to `<docs>/_users/Chats/...` (an owner called `Chats`); it is refused now (the target must be canonical). Nothing else differs on
  the tests above (the new spelling tests pass on the old code for the migrated helpers).
- This commit also carries the workspace half of step 2 (`slots.ExecConfig.SlotForLaunch`, `slottmux` follows the launch script's run folder,
  `slots/launch_test.go`); the agent_go and provider halves, and what the fallback status is, are under Step 2 once that lands.
