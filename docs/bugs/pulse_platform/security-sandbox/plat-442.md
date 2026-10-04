# PLAT-442 — Identity is explicit: owner from the manifest, slot named by the platform, Crews at `Crew/<id>`

Status: open, approved by the owner 2026-10-04; nothing built. Follows PLAT-435 (one path type in agent_go).

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

Nothing yet.
