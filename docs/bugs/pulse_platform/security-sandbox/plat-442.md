# PLAT-442 — Identity is explicit: owner from the manifest, slot named by the platform, Crews at `Crew/<id>`

Status: steps 1, 2, 3 and 5 on main (2026-10-04), not deployed. Step 4 (the Crew move) is not started. Crew CLI identity needs a decision: PLAT-446. Follows PLAT-435 (one path type in agent_go).

## Problem

Who owns a project and which Linux slot a CLI runs as are both read out of the folder path (`_users/<id>/...`), in three separate copies of the rule:
agent_go (now `pkg/workspaceref`), the `workspace/` module (`slots.SlotForDir`, `slots.IsCodeProjectDir`, `utils/path.go`), and the provider
(`internal/slotfs.SlotOf`). Consequences found 2026-10-04:
- A Crew Run-mode reader's turn runs in the owner's folder, so it runs as the **owner's slot**. That is the owner's choice (below), but it was implicit.
- Goals (`Workflow/<name>`) have no `_users/` segment, so they run as the app account. Also the owner's choice (below), also implicit.
- Moving Crews to a shared `Crew/<id>` (crew_shared_root.md) would silently drop their slot isolation: the path would no longer name an owner.

## Decisions (owner, 2026-10-04)

1. Crew turns (owner and reader) run as the **app account**, as today (corrected 2026-10-04 by PLAT-446: the CLI starts in an app-owned runtime folder, not in the owner's folder); the reader block, tools and folder guards limit the reader.
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

### Step 2: the platform names the slot (2026-10-04, not deployed)

Provider `ae8e204` (pinned in `agent_go/go.mod`), agent_go and workspace commits below.

**What it was, proven first.** The provider's `slotfs.SlotOf(hint)` and the tmux front-end's `ExecConfig.SlotForDir(dir)` took the slot of the
user whose `<docs>/_users/<id>/` tree the folder lies in (plus the canary `AGENTWORKS_SLOT_CLI_USERS`, checked against that id). Regression
tables written and run on the unchanged code before anything moved: `internal/slotfs/slotfs_identity_test.go` (provider; first version passed on
04645b8), `workspace/slots/identity_table_test.go` (also run against the pre-change workspace code), and `cmd/server/multiuser_identity_test.go
TestRunAsRegressionTable`, which builds the folder each turn kind REALLY starts its CLI in with the chat handler's own code and checks that the
platform's declaration equals what the old folder rule gives for it:

| Turn | CLI starts in | Slot (unchanged) |
|---|---|---|
| Code, owner A | the project folder, `_users/A/Chats/Code/projects/p` | A's slot |
| Crew, owner A | isolated runtime `<app state>/cli-runtimes/v1/<hash>` (links to the project) | none: the app account |
| Crew, reader B (Run mode) | the same kind of runtime folder | none: the app account |
| Goal, any user | isolated runtime (chats) or `Workflow/<name>` (runs) | none |
| private chat of A | `_users/A/Chats` | A's slot |
| delegated sub-agent of A | A's runtime folder in `_users/A` | A's slot |

**Found: this is not what decision 1 assumed.** The ticket (and decision 1) say a Crew reader's turn runs as the owner's slot because it runs
"in the owner's folder". It does not: since the Crew CLI isolation (`crewCLIWorkingDir`, always on for coding CLIs) the Crew CLI never starts in the
Crew folder, so Crew turns of owner and reader both run as the app account. Only the bridge shell tool (workspace service, `slots.For(X-User-ID)`)
runs as the caller's slot. The task was to make the identity explicit without changing it, so the platform now DECLARES the app account for Crew
turns (`run_as.go`, one table in the comment on top). Making Crew turns run as the owner's slot is a behaviour change (the runtime folder is
app-owned 0700, so the CLI could not even enter it as the slot); it needs its own decision. See PLAT-446.

**How the slot is carried.**
- `llmtypes.RunAs{Declared, User, Slot, Root}` (provider). Carried on the launch policy (`CLISecurityPolicy.RunAs`, so it travels through
  mcpagent unchanged) AND declared for the CLI's folder in a small in-process registry (`llmtypes.DeclareRunAs(dir, RunAs)`, longest-folder
  match, bounded). The registry is needed because a launch without a Landlock policy has no policy to carry it, and because the provider asks
  about many folders (working dir, private home, launch files) under the turn's folder.
- agent_go decides per turn in `decideTurnRunAs` (`run_as.go`): Code and Crew take the owner from the manifest (step 1), a Goal and an isolated
  runtime are the app account, a private chat is its user. Called from the chat handler right after the policy is resolved, from the workflow
  orchestrator (Goal: declared app account for the workflow folder) and from delegation (sub-agent runtime folder).
- `slotfs.SlotOf` order: the slot named in the path (a folder inside `<slot state root>/<slot>` or `<run root>/<slot>`, explicit by construction) ->
  the declaration (checked against the host's slot table, which wins on disagreement, logged `[SLOT_EXPLICIT_MISMATCH]`; the canary is applied to
  the declared user) -> the old folder rule, logged once per folder as `[SLOT_FALLBACK] path-inferred slot ...`.
- The tmux front-end (`slottmux`) no longer reads the folder at all: it runs a session as the slot whose run folder holds the launch script, which
  the provider only creates there for a launch it decided runs as the slot (`SlotForLaunch`). A folder that names another slot than the script is
  refused (`[SLOT_EXPLICIT_MISMATCH]`). One difference: a Muse launch for a slot user whose script is not in the slot folder now gets the Muse
  launch log (before it did not).

**Fallback status.** Not removed. Launches that declare nothing still fall back (logged): CLI launches outside the chat handler, orchestrator
and delegation: `workspace_advanced_tools` (working dir is the docs root, names no user), `internal/agentsession` (SparkQuill/Family sessions,
`cfg.WorkingDir`), the provider-account login confinement (`provider_setup_confine.go`), and the Goal phase-switch re-launch
(`SetCodingAgentWorkingDir(phaseCLIWorkingDir)` is a runtime folder, no slot either way). Look for `[SLOT_FALLBACK]` in the logs after a deploy;
when none appears over a canary period the fallback can go.

### Step 5: multi-user fixture and tests (2026-10-04, not deployed)

`cmd/server/multiuser_fixture_test.go` (reusable helper) and `multiuser_identity_test.go`. Fixture: users A and B (both hold a slot in a temp slot
table), a temp docs root and CLI state root, the existing `mockWorkspaceAPI` as the workspace service, a Crew and a Code owned by A (manifests
with `owner_id`), a Goal A owns and B reads. No real CLI, no network. `identityLayout` says where the projects live (`legacyIdentityLayout()`
= today's per-user folders); `runMultiUserAccessAssertions(t, layout)` runs every access assertion for a layout, so the Crew move runs the same
assertions with a layout whose `CrewPhysical` and `CrewShort` return `Crew/<folder>`. `identityExpectation` (`todayIdentity`) states who each
turn kind runs as, in one place.
- B cannot reach A's Code by either spelling (binding, `conversationTargetAccess` with and without the profile, proxy read and write); A reaches it
  by both.
- B in Run mode: the physical Crew path resolves with owner A and reader access, `isCrewReaderTurn`, reader roots have no write path, the proxy
  refuses B's write; A reaches the Crew by the short spelling as owner.
- Run-as per turn (explicit): Code A -> A's slot; Crew owner and Crew reader B -> app account (see PLAT-446); Goal -> none; private chats ->
  their user. Each row is also compared with what the old folder rule says for the CLI's real starting folder.
- Workspace proxy decision for each user and path pair (A and B x Code, Crew, Goal x read/write, both spellings).
- Not covered (needs a real OS): the Linux identity of the launched process; that stays with the Linux slot suites on a host.

## Test status (2026-10-04)

agent_go `cmd/server` full run: the same 13 failures as on the baseline (Relay catalog, `TestPrivateCodeCallerIsSeparateFromCrewWithSameProjectID`,
`TestSalesCrewCatalogHasInstallableRoles`, `TestResolveDelegationTierConfigExpandsProviderProfile`, the playbook catalog pair,
`TestAgentWorksProductSurfaceE2E`, `TestCrewProductSurfaceE2E`, the three provider-accounts tests, `TestNativeTerminalRealTmuxKeyboardAndPaste`,
`TestWorkshopResolveLLMConfigExpandsCodingAgentMode`); none new. `TestExternalToolsClientTransportThroughJWTAndWorkspace` failed once in a
loaded full run and passes alone and on rerun (timing). Workspace module: all green except `TestUserCaptureStaleStateStartsFreshAndRejectsBlankEvidence`
(browser IPC, fails the same on the baseline). Provider: `llmtypes`, `internal/slotfs`, `clisandbox`, `shelllaunch` and the root package pass.

## Left

- Step 4, the Crew move (not started, by instruction).
- Fallback removal: launches that declare no run-as still fall back to the folder rule (logged `[SLOT_FALLBACK]`); see Step 2 for the list. Remove
  it once a canary period shows none in the logs.
- UI-created Crews and Codes have no `owner_id` until the owner first opens them or the next start (startup scan); the browser could write it
  at creation.
- PLAT-446: owner decision on Crew CLI turns (app account today).

## Risks for step 4 (the Crew move)

1. Crew CLI turns never ran as a slot (PLAT-446), so the move cannot lose that isolation; but the BRIDGE shell tool runs as the caller's slot via
   `X-User-ID`, so for a reader it runs as the reader's slot in the owner's folder. Check that the move does not change which folder that slot
   can enter (group ACLs follow the owner's tree today).
2. `resolveCrewPath` already prefers the manifest owner, but `crewProjectOwnerID(path)` (path only) still feeds about ten callers (schedules,
   triggers, webhooks, reader chat mirror, bot scope, `routeWorkspaceUserID`); each must move to the manifest or it will say "no owner" for
   `Crew/<id>`. `resolveProjectOwner(ctx, root)` (`product_owner.go`) is the helper.
3. The raw proxy refuses foreign `_users/*` today and that is the ONLY thing keeping a reader out of the Crew's files; `Crew/<id>` has no such
   rule until the proxy gates it like `Workflow/` (`workspaceProxyPolicy.denies`). The fixture's "B cannot write the Crew" and proxy rows are the test
   for it; rerun `runMultiUserAccessAssertions` with the `Crew/<id>` layout before enabling the move.
4. `cleanAgentProfileWorkspace` and the live feed's visibility check name the path owner; both need the manifest owner for shared roots.
5. The provider's folder rule is now only a fallback, so a Crew in `Crew/<id>` does not silently get a different slot; it gets what
   `decideTurnRunAs` declares (the app account for the isolated runtime). If the owner chooses the owner's slot (PLAT-446), declare it there.
6. A Crew's CLI runtime folder hashes the project path (`cliruntime.Prepare` digest of user, workflow, session, provider, mode): moving the Crew
   changes the hash, so every existing CLI session of a Crew would start a fresh runtime (the digest input is kept for existing chats, see the
   comment in `cliruntime/workspace.go`). The migration must alias the old path as the digest input or accept one fresh session per Crew.
7. `owner_id` backfill: a manifest copied between accounts keeps the old owner; `[OWNER_MISMATCH]` in the logs lists them before the move.

## 2026-10-04 independent review

Reviewed AgentWorks `a04b393c9` with provider `ae8e204` and mcpagent `ffc32d7`.
Do not treat the current ownership/identity implementation as ready for rollout:
[PLAT-449](plat-449.md) reproduces editable owner metadata selecting another
user's slot; [PLAT-450](plat-450.md) reproduces cross-user manifest mutation
through a symlink in startup backfill; [PLAT-451](plat-451.md) traces a slot
mismatch falling through to app-account tmux rather than rejecting the launch.
No implementation fix or live server change was made in this review.

Parser/guard tests, workspace slots/utils tests, provider llmtypes/slotfs tests,
and the focused application owner/run-as/multi-user tests passed. Temporary
probes confirmed the first two findings and were removed after verification.
The broader selected server suite also had three failures already documented
in PLAT-435: `TestPrivateCodeCallerIsSeparateFromCrewWithSameProjectID`,
`TestSalesCrewCatalogHasInstallableRoles`, `TestCrewProductSurfaceE2E`.
This review did not rerun their old baseline or a real Linux CLI launch.
The shared Crew-root move remains step 4, not implemented by these commits.
