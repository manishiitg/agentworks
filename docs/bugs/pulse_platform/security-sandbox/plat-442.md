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
