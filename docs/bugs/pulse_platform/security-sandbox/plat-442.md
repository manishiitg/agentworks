# PLAT-442 — Identity is explicit: owner from the server's registry, slot named by the platform, Crews at `Crew/<id>`

Status: steps 1, 2, 3 and 5 on main (2026-10-04), not deployed; step 1's owner source was CHANGED the same day by PLAT-449 and PLAT-450 (registry, not manifest; no symlinks, see below). Step 4 (the Crew move) is BUILT and tested on temp trees (2026-10-04) and NOT run against any real data, not deployed; it is inert until the migration is run and the creation switch `AGENTWORKS_CREW_SHARED_ROOT=on` is set. Crew CLI identity: PLAT-446 (decided: the app account). Follows PLAT-435 (one path type in agent_go).

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

### Step 1: owner of a project (2026-10-04, not deployed) -- REVISED by PLAT-449 / PLAT-450

**Superseded decision.** The text below describes what step 1 first did: trust `product.json`'s `owner_id` over the path. That was wrong: the manifest is
user-writable project data (a user, or a Crew's own agent turn with write access to the project, can edit it) and it chose a Linux slot
([PLAT-449](plat-449.md)); and the backfill followed symlinks ([PLAT-450](plat-450.md)). Now: ownership is **server-controlled** metadata in a registry in the app's
state area (`<state root>/ownership/projects.json`, outside the docs root); resolution is registry entry, else the physical path owner, else (shared `Crew/` with no
entry) nobody; `owner_id` in `product.json` is written but is information only and is never trusted (a disagreement is logged `[OWNER_MISMATCH]`, registry or path
wins). A private Code whose registered owner is not the admitted caller is refused before any CLI starts. The startup scan, the owner opening a project, server-side
creation and the Crew move write the registry; every manifest read/write is an anchored open that refuses symlinks. See the DECISIONS entry "Project ownership is
server-controlled". The paragraphs below are kept as history where they still describe the code; read "owner from the manifest" as "owner from the registry".

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

### Step 4: the Crew move (built 2026-10-04; not run on any real data, not deployed)

Crews move from `_users/<owner>/Chats/Work/projects/<slug>-<id8>` to the shared `Crew/<slug>-<id8>` (folder name kept) by a migration command, with the code
that makes `Crew/<id>` a first-class place. Code, SparkQuill, Video Studio and Goals do not move. Ownership is the server's registry (PLAT-449), not the
manifest the original design relied on (the design in `docs/design/crew_shared_root.md` is superseded where it says "owner from product.json", "alias file
in `_system/`", "rewrite stored references" and "migrate at startup": this step keeps every stored reference as it is and serves it through an alias, and
the move is an explicit, backed-up, journaled command).

**Safety order (what was built first).** The gate came first, with failing tests: today a foreign `_users/*` refusal is the only thing keeping a reader out of
a Crew's files, and `Crew/<id>` has no `_users` segment. `crew_shared_root_gate_test.go` drives the raw proxy's gate with every request shape (URL routes,
query parameters, JSON body fields including nested and array ones, multipart, absolute/backslash/dot spellings) for the owner, a Run-mode reader, a user
without the Crew product, an administrator who is not the owner and another Crew's owner, plus a parity test against the same Crew in the owner's tree. It
found PLAT-456 at once. A second gate was needed that the design did not list: the workspace service's symlink guard only protects `_users/<id>` trees, so
a link made anywhere could read a Crew at the shared root (PLAT-457; closed for `Crew/<id>` here).

**What exists now**

- `workspaceref`: a shared root (`SharedCrewRoot`, `Ref.IsShared/SharedProject/SharedProjectRoot/SharedTree/AnyCrewProject`). A shared path names no owner, is
  never "the caller's own" or "public" (`OwnedByOrUnowned` is false for it), `Physical`/`CanonicalFor` never place it under a user, and `Project()` does not
  return it (so `ref.IsProject()` callers can never treat `Crew/x` as the caller's). Table tests; the guard test still passes.
- Owner: `resolveProjectOwner` (registry; shared root with no entry = nobody). `crewProjectOwnerID`, `isCrewProjectPath`, `crewProjectOwnedByCaller`,
  `canonicalCrewWorkspaceRoot` are shared-root and alias aware, so the ~35 callers of them follow without change.
- Binding: `resolveProductProjectBindingWithStore` finds the caller's Crews at `Crew/` by registry owner after their own tree; `resolveCrewProjectBinding`
  finds another owner's (reader, only while project sharing is on, as before); a project found in the caller's tree that is registered to someone else is
  refused. Every enumeration of "this owner's Crews" asks both places (`listProjectManifestPaths`, `listSharedCrewManifestPaths`).
- Aliases: the registry record of a moved Crew carries its old physical roots as `aliases`; `crewPathAliases` reads them (not a file in `_system/`, which
  an agent could not write either but the registry is the one authority). One resolver (`resolveCrewPath`) follows them for the physical and the caller's
  logical spelling, with a logged `[CREW_ALIAS]` warning once per old path. `pkg/wsalias` is the same translation at the workspace transport (server client,
  tool client, browser proxy): an old path in a request is rewritten to `Crew/<f>` before it leaves, so a read finds the Crew and a write never creates a folder
  at the old place. The proxy gate classifies the translated path, so an old spelling is judged as the Crew it names.
- `crewAccessFor`: another user is a reader only while project sharing is on (default off); with it off nobody else sees the Crew or its live-feed notices.
- Creation: `AGENTWORKS_CREW_SHARED_ROOT=on` makes server-side creation (`create_crew`, import, external authoring) put new Crews at `Crew/<slug>-<id8>`, registered
  to the creator, with the owner's slot group set (`ensureSharedCrewFolderAccess`); default off, nothing changes by deploying. Browser creation writes
  into the owner's tree from the browser and still does; a later run of the migration moves it. Reading `Crew/<id>` never depends on the switch.
- UI: the Crew list asks `GET /api/agent-profiles/work/own-shared-projects` (the caller's own Crews at the shared root, by registry) next to the listing of
  their own tree; `projectProductForPath`, the report page path check and the cost rows know `Crew/<id>`. Old servers answer 404 and the list is unchanged.

**The migration command** `agentworks server migrate-crews-to-shared-root` (`crew_move*.go`; flags `--docs-root --state-root --dry-run(default) --apply
--backup-dir --crew (repeatable) --rollback <folder> [--from-backup] --finalize --no-reference-scan --json`):

- Dry run (default): every Crew with owner, source, destination, size, files/folders/symlinks, registry and manifest owner, the users who would read it, CLI runtime
  folders that link to it, stored references that will be served through the alias (counts of files per store kind; nothing is rewritten), warnings and
  BLOCKERS. It changes nothing (tested by comparing complete snapshots of the docs tree and state area).
- Blockers (they block `--apply` for that Crew only; the rest proceed and the run exits non-zero): `[OWNER_MISMATCH]` (manifest `owner_id` differs from the owner the path
  names: resolve by confirming the owner and fixing `owner_id` in `product.json`), `Crew/<folder>` exists (never merged), the same folder name under several
  owners, `[ACTIVE]` (a tmux pane or a process works in the Crew or in a CLI runtime folder that links to it), a symlink that leaves the Crew folder or a special file,
  `[UNSAFE_PATH]` (a symlink where a folder or the manifest should be), a registry owner that disagrees with the path.
- `--apply` needs `--backup-dir`: free space is measured, exactly the Crew folders about to move (and the registry) are copied to
  `<dir>/crew-move-<time>/` and READ BACK and compared with the source before the first move; any failure aborts with nothing changed.
- One Crew at a time, copy → verify → switch: copy into `Crew/.migrating/<folder>` (modes, groups and times kept; symlinks copied AS symlinks; nothing is followed:
  every access is through `os.Root`), hash both sides and compare entry by entry, re-check the source did not change, then two renames inside the docs root
  (staging in, old folder renamed to `_users/<owner>/Chats/Work/.moved-to-crew/<folder>`), then the registry marks the Crew shared with the old path as an alias, runtime
  links are repointed, and the moved Crew is re-hashed against the journal with the owner, alias and access rules checked as the server applies them.
  Nothing is deleted until `--finalize`.
- A journal per Crew (`<state root>/migrations/crew-move/<folder>.json`, written before and after every step) makes a crash resumable: tests crash at every
  named point and the re-run finishes with nothing lost or duplicated. Re-runs are idempotent. A lock marker (`active.json`) makes the server bind no turn, schedule,
  webhook or bot to that Crew and the proxy refuse writes to it while it moves; a marker of a dead process locks nothing.
- `--rollback <folder>` moves the Crew's CURRENT folder back (work done since the move is kept), unmarks the registry, repoints runtime links back;
  `--from-backup` restores the backed-up copy instead (the damaged folder is kept aside by name). `--finalize` removes the kept old folders of finished moves.

**Decisions made while building (behaviour; see DECISIONS)**

- Old paths resolve for good through the registry's aliases; no stored reference is rewritten.
- A moved Crew keeps its CLI runtime folder: the runtime folder is named by a digest of the project's resolved path, so a moved Crew would have started a
  fresh native session in every chat. `cliruntime.PrepareLinkedProjectMoved` keeps the OLD path as the digest input (from the registry alias) and
  repoints the runtime's `project` link (only from exactly the old target); the migration repoints existing links too, so the link also works if the
  server starts before it. Tested: the runtime folder is identical before and after, the native session file in it is still there, other Crews' runtimes do not change, a new
  Crew at the shared root gets its own.
- A moved Crew keeps its browser profile key (its first old physical path), under every spelling, so saved logins and tabs survive.
- Cost rows recorded before the move fold into the Crew's row.

**Every site of the old path rule, and how it was handled** (grep for `crewProjectOwnerID`, `crewProjectOwnedByCaller`, `isCrewProjectPath`,
`Chats/Work/projects`, `CrewProjectsRoot`, `Chats/Work`, `ProjectsRoot`, `.Project()`):

| Site | Handling |
|---|---|
| `workspaceref` (both modules) | shared root semantics (above) |
| `workspace_proxy.go` gate, `workspace_proxy_policy.go` | `Crew/<id>`: owner by registry only, bare root and hidden entries never, absolute/backslash spellings (PLAT-456), old spellings judged as the translated Crew, writes refused while the Crew moves |
| `workspace/utils/path.go` (`IsValidFilePath`) | links into a Crew tree from elsewhere refused (PLAT-457) |
| `workspace/handlers/database_backup_snapshot.go` | `Crew/` is a managed root for the DB backup snapshot |
| `agent_profile_runtime.go` `cleanAgentProfileWorkspace` | accepts `Crew/<id>` for its registry owner only; the query path runs a verified binding root and rejects a client folder that is not the same Crew (alias-aware) |
| `crew_access.go` (`crewProjectOwnerID`, `isCrewProjectPath`, `crewProjectOwnedByCaller`, `canonicalCrewWorkspaceRoot`, `resolveCrewProjectBinding`) | rewritten (above); their callers need no change: `crew_functions.go`, `crew_session_mode.go`, `product_schedules.go`, `product_webhooks.go`, `cost_overview.go`, `pulse_crew_calls_tool.go`, `instructions.go`, `work_schedule_tools.go`, `crew_workflow_tools.go`, `gmail_*`, `browser_live.go`, `trigger_link_tools.go`, `crew_reader_chat_mirror.go`, `work_workflow_reference_tools.go`, `agent_profile_runtime.go` |
| `crew_ref.go` (`resolveCrewPath`, `crewAccessFor`, owner cache, alias cache, `followCrewAlias`, `crewTurnWorkspace`, `foldCrewRootForStoredReference`) | registry owner; alias from the registry; no negative owner caching |
| `product_conversation_registry.go` (binding in root, shared lookup, lock check) | shared root found by registry owner; refuses while the Crew moves |
| `chat_history_persistence.go` (`workspacePathsMatchForUser`, `canonicalChatHistoryWorkspacePath`), `agent_profile_conversations.go` (`normalizeConversationWorkspace`) | fold old spellings of a moved Crew to `Crew/<f>`: every chat-history, bot-scope, browser, resume comparison keyed on them follows |
| `product_schedules.go`, `product_webhooks.go`, `work_workflow_reference_tools.go`, `crew_directory.go`, `crew_delete_references.go`, `chat_submission_journal.go`, `code_peer_functions.go` | enumeration/authorization asks both places (`listProjectManifestPaths`, `listSharedCrewManifestPaths`, `sharedCrewOwner`) |
| `workflow_context_access.go`, `external_file_reads.go`, `workflow_read_access.go`, `pkg/workflowtypes` attachments, `step_based_workflow/workflow_folder_access.go` | stored attachments keep their stored spelling and resolve to the Crew's current root; the stored root and the freshly authorized binding root compare equal |
| `cost_overview.go` | old rows fold into the Crew's row |
| bots: `services/bot_scope.go`, `services/slack_connections.go`, `services/bot_connector.go`, `services/whatsapp_service.go`, `slack_connection_routes.go`, `crew_bot_scope_migration.go`, `bot_manager_wiring.go` | `Crew/<f>` is a valid destination whose owner is the registry's; old and new spelling are one destination; the WhatsApp list includes the user's shared Crews |
| `place_mcp_attach.go` | old roots fold to the Crew's new root; stored entries merge in memory |
| `browser_conversation_isolation.go`, `browser_workspace.go` | browser profile key kept; project-root check knows the shared root |
| `crew_cli_runtime.go`, `pkg/cliruntime` | runtime folder (digest) kept; link repointed |
| `crew_creation.go`, `external_crew_authoring.go`, `crew_shared_access.go` | creation switch; registry registration; slot group; `Crew/` mode 0711 |
| `agent_profile_conversations.go` delete | registry entry removed with the Crew |
| `pkg/common/session_workspace.go` | `Crew/<f>` sessions classify as Crew projects |
| `pkg/wsalias`, `workspace_http.go`, `pkg/workspace/client.go`, proxy transport | old paths translated at the workspace transport |
| UI: `projectProduct.ts`, `productProjects.ts`, `workSessions.ts`, report page, costs | see above |
| NOT touched (path-independent or not Crews) | schedules/triggers state (keyed by manifest id), Slack thread bindings, structured chat events, `work:project:<id>` and `product-<uuid>` session ids (they embed the project id), Code/SparkQuill/Video Studio/Goals, `docs/design/crew_shared_root.md` (superseded, stale status) |

**Stored references, each tested with the old path after a real migration** (`TestStoredReferencesToAMovedCrewKeepWorking`, `TestMovedCrewStoredReferences...`,
`TestAccessAssertionsAfterARealMove`, `TestProxyTranslatesOldSpellingsOfAMovedCrew`): typed physical and logical paths, chat history workspace keys, a
workflow's attached Crew (`workflow.json` attachments and context paths, including a reader's), a bot destination (valid, same destination, owner), a turn
that still names the old folder, raw read access and the proxy by the old path, external file roots, place MCP roots, the browser key, cost rows, an
unmigrated Crew untouched.

**Access, the same assertions on every layout.** `runMultiUserAccessAssertions` runs on the old layout, on `Crew/<id>`, on a mixed server (one Crew moved, one
not) and after a REAL migration of the fixture's files: A (owner) by the short spelling, B (Run-mode reader, sharing on) by both, C (no Crew product) refused
everywhere (binding, access level, live feed, raw proxy), B's writes refused (proxy, tool surface, shell folder guard), sharing off refuses B, Code and Goals unaffected.
The bridge shell tool runs as the caller's slot: the migration preserves every file's mode and group (and widens only the owner's slot group's bits on files
the slot account owned), no entry gets a bit for "other", folders keep setgid, and `Crew/` is 0711 (traversable, not listable); tested on temp trees. Not tested:
the Linux identity of a real slot shell (needs a host).

**Test status for step 4** (see the final report of the session for counts): targeted runs per commit; full `cmd/server` once at the end; the 13 baseline failures
(Relay catalog, `TestPrivateCodeCallerIsSeparateFromCrewWithSameProjectID`, `TestSalesCrewCatalogHasInstallableRoles`,
`TestResolveDelegationTierConfigExpandsProviderProfile`, the two playbook tests, `TestAgentWorksProductSurfaceE2E`, `TestCrewProductSurfaceE2E`, the three
provider-accounts tests, `TestNativeTerminalRealTmuxKeyboardAndPaste`, `TestWorkshopResolveLLMConfigExpandsCodingAgentMode`) are unchanged.

**What was NOT verified (nothing here ran on a real server)**: the move on real data (sizes, run time, a 12-Crew Confida tree), Linux slot accounts and group/ACL
behaviour of a real `Crew/` folder (the unit tests check modes and groups on temp trees only), tmux/process detection against real CLI sessions on the target
hosts (tested with a real child process and a stubbed tmux), the app account's ability to `chgrp` to the slot groups (the move fails the Crew with a clear
message when it cannot keep a group), a remote (non-local) workspace service (the command works on the docs folder directly and refuses nothing about it),
the frontend in a browser (vitest and `tsc -b` only), and a live CLI session surviving the runtime folder repoint.

#### Runbook (per server; Confida first)

Order matters. Stop conditions are at the end. Confida: 12 Crews, 2 Codes (the Codes are not touched by anything below); RTS and excellence: after Confida is
clean for a few days.

1. **Before anything.** Deploy this release with `AGENTWORKS_CREW_SHARED_ROOT` unset (nothing changes by itself; the startup scan registers every Crew and Code in
   the owner registry from its path: read `[OWNER_BACKFILL] ... conflicts=N unsafe=N` and any `[OWNER_MISMATCH]` / `[UNSAFE_PATH]` lines; do not "fix" them on live
   data without confirming the intended owner). The registry is `<state root>/ownership/projects.json`; include the state root in the host's backups.
2. **Room.** Docs volume free space at least the total size of the Crews to move plus 10% (the old folders stay until `--finalize`); the backup directory free space the
   same again. `df` both. Pick a quiet window: no Crew turns, schedules or webhooks due for the Crews being moved (the dry run shows live tmux sessions and processes;
   stop the Crews' schedules if one is due, or stop the server for the window: the command works on the docs folder, not through the server).
3. **Backup of the host** (your normal snapshot) AND the command's own: `--backup-dir` on a different volume.
4. **Dry run, everything**: `agentworks server migrate-crews-to-shared-root` (same environment as the server: `WORKSPACE_DOCS_PATH`, `AGENTWORKS_STATE_ROOT`).
   Read every Crew's block. Resolve every BLOCKED line (an `[OWNER_MISMATCH]`: confirm the owner, fix `owner_id`; an `[ACTIVE]`: stop what is running; a collision or symlink:
   look at it by hand). Read "references": those are what is served through the alias.
5. **Apply ONE Crew first**: `... --apply --backup-dir /backups/crews --crew <folder>`. It prints the backup directory it made and verified, the move, and `verified`.
6. **Check that Crew in the app**: open it as the owner (chat resumes the same native session: the runtime folder is unchanged), its files, dashboard, schedule, a bot
   route if any; as a reader (only where project sharing is on) read-only; in another account without the Crew product: nothing. Look at the server log for
   `[CREW_ALIAS]` lines (old paths in use: expected) and any `[OWNER_MISMATCH]`.
7. **Apply the rest** (same command without `--crew`). A re-run after any interruption is safe and resumes.
8. **Verify**: re-run the dry run (every moved Crew shows state `done`); the report's `verified` list; `find <docs>/Crew -maxdepth 1` lists the Crews and `.migrating` is empty;
   `ls -ld <docs>/Crew` is `drwx--x--x`; each Crew folder keeps its old group (`ls -ld <docs>/Crew/<f>` vs its tombstone).
9. **Turn the switch on**: set `AGENTWORKS_CREW_SHARED_ROOT=on` in the server's environment and restart in a quiet moment (new server-side Crews are then created at
   `Crew/`). Browser-created Crews still land in the owner's tree; the next run of the command moves them.
10. **Watch** for a few days: `[CREW_ALIAS]` (stale references: expected, harmless), `[OWNER_MISMATCH]`, `[UNSAFE_PATH]`, `[CREW_ACCESS]` (slot group could not be set on a new
    Crew), `[OWNER_REGISTRY]`; chats resuming; schedules firing for moved Crews.
11. **After the soak** (say a week): `--finalize` removes the kept old folders (it re-verifies each moved Crew first). Keep the backup directory until you are sure.

**Rollback**: one Crew: `... --rollback <folder>` (keeps work done since the move; the registry unmarks it; runtime links go back); if its new folder is damaged
`--rollback <folder> --from-backup --backup-dir <the dir of the run>`. All: roll back each Crew; then unset `AGENTWORKS_CREW_SHARED_ROOT`. The release can stay deployed:
unmoved Crews are the old behaviour.

**Stop conditions** (stop, do not continue to the next step): a verification failure in the report or the log; a blocker you do not understand; any Crew that fails its
move (the run stops at the first failure and leaves it resumable: read `last_error` in its journal before re-running); free space under the estimate; a Crew
opened as its owner that does not find its chat history or files; a reader or an outsider who can see a Crew they should not; `[OWNER_MISMATCH]` for a Crew that has already moved.

## Test status (2026-10-04)

agent_go `cmd/server` full run: the same 13 failures as on the baseline (Relay catalog, `TestPrivateCodeCallerIsSeparateFromCrewWithSameProjectID`,
`TestSalesCrewCatalogHasInstallableRoles`, `TestResolveDelegationTierConfigExpandsProviderProfile`, the playbook catalog pair,
`TestAgentWorksProductSurfaceE2E`, `TestCrewProductSurfaceE2E`, the three provider-accounts tests, `TestNativeTerminalRealTmuxKeyboardAndPaste`,
`TestWorkshopResolveLLMConfigExpandsCodingAgentMode`); none new. `TestExternalToolsClientTransportThroughJWTAndWorkspace` failed once in a
loaded full run and passes alone and on rerun (timing). Workspace module: all green except `TestUserCaptureStaleStateStartsFreshAndRejectsBlankEvidence`
(browser IPC, fails the same on the baseline). Provider: `llmtypes`, `internal/slotfs`, `clisandbox`, `shelllaunch` and the root package pass.

## Left

- Step 4: built; run it per the runbook, one server at a time (Confida first), and turn the creation switch on afterwards. Not run on any real data.
- Fallback removal: launches that declare no run-as still fall back to the folder rule (logged `[SLOT_FALLBACK]`); see Step 2 for the list. Remove
  it once a canary period shows none in the logs.
- UI-created Crews and Codes get their registry entry when the owner first opens them or at the next start (startup scan, from the path); the browser still
  creates Crews in the owner's tree even with `AGENTWORKS_CREW_SHARED_ROOT=on` (creation through a server endpoint would put them at the shared root; until
  then the migration command moves them on its next run).
- PLAT-446: decided (app account); nothing left.
- PLAT-457: the symlink rule for `Workflow/<id>` and `config/`.
- The old folders (`.moved-to-crew`) and the backup directory are kept until `--finalize` / by hand; nothing deletes them on its own.
- `pkg/common` browser checkpoint classification (`CanonicalSessionWorkspace`) does not fold an old spelling of a moved Crew (new sessions use the new path).

## Risks for step 4 (the Crew move), and where each stands

1. **Bridge shell tool as the caller's slot** (a reader's commands run as the reader's slot): handled by preserving every file's mode and group and giving `Crew/` mode
   0711 (no "other" bits anywhere in a moved Crew, group read/write for the owner's slot, setgid folders), asserted on temp trees; the reader's folder guard is read-only
   as before. NOT verified with real accounts; check `ls -ln` of a moved Crew against its tombstone on each host before turning the switch on.
2. **`crewProjectOwnerID(path)` feeding ~10 callers**: fixed at the function (shared-root and alias aware, registry owner), so every caller follows; the list is in the site table.
3. **The proxy's refusal of foreign `_users/*` was the only thing keeping a reader out of a Crew**: `Crew/<id>` has its own gate (owner by registry only; bare root, hidden
   entries, ownerless Crews, absolute/backslash spellings refused), tested on every request shape and by parity with the owner's tree; plus the symlink rule (PLAT-457).
4. **`cleanAgentProfileWorkspace` and the live feed's visibility**: both use the registry owner for shared roots; the live feed shows another user's Crew only while
   project sharing is on.
5. **CLI runtime digest hashes the project path**: kept (old path as the digest input), tested; runtime links repointed.
6. **A manifest copied between accounts keeps its old `owner_id`**: now harmless for ownership (the registry decides) and reported by the dry run as `[OWNER_MISMATCH]`, which
   blocks that Crew's move until a person resolves it.

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

## 2026-10-05: first dry run on a real host (RTS) and what it changed

RTS dry run (read-only): 3 Crews (ci-cd 0.9 GiB, gptlive1 7.2 GiB / 307k files, rts-flow-tester 1.1 GiB), all registry-consistent, 0 could move: each had symlinks that leave the Crew folder. Found: CLI login links inside `.sandbox-cache/cli-home/*` pointing at the app account's own login files (`/var/lib/video-studio/.claude/.credentials.json`, `.cursor/...`), a link into `/tmp/aw-browser-0/...`, a link to another path in the same Crew by absolute spelling, and virtualenv/`.bin` links to `/usr/bin/python3`. Decision made while fixing: `crewLinkEscapes` now allows absolute links into read-only system trees (`/usr`, `/bin`, `/sbin`, `/lib`, `/lib64`: copied as links, never followed); everything else that leaves the folder still blocks. The login, `/tmp` and absolute self-links are removed on the host before the move (the CLI recreates the login links at its next launch). Owner instruction 2026-10-05: do the move on RTS first, without waiting for him.

## 2026-10-05: the first real moves on RTS (ci-cd, rts-flow-tester moved; gptlive1 pending)

`ci-cd` (0.9 GiB) and `rts-flow-tester` (1.1 GiB) moved to `Crew/<folder>` on RTS after a verified backup each; checked on the host: the moved folder is `video-studio:slot01 drwxrws---`, identical to an unmoved Crew; `Crew/` is `drwx--x--x`; no `[CREW_ALIAS]`/`[OWNER_MISMATCH]`/`[UNSAFE_PATH]` lines logged. `gptlive1` (7.2 GiB, 307k files) aborted twice IN THE BACKUP, nothing changed (both safe failures): (1) a chat-history file changed during the copy (a one-off write), (2) after ~22 minutes `db/db.sqlite-shm` disappeared between listing and copy (SQLite creates and deletes that shared-memory index as processes open/close the Crew's database). Fix: the walk now skips `<db>-shm` when `<db>` exists (it holds no data; reported as a warning); the database and its `-wal` are still copied and must not change. Costs learned: the backup of 7.2 GiB / 307k files takes ~20+ minutes on RTS (2 vCPU) before anything moves; failed attempts leave partial backup folders without `backup.json` (removed by hand on RTS; the command should clean them up).

Third `gptlive1` abort (backup, after 2 min): `.report-cache/.runtime/db-read-snapshot-<n>.sqlite` deleted mid-copy (the report runtime's throwaway snapshots). Fix: the walk skips `.report-cache/.runtime` entirely (reported as a warning, recreated on demand); the BACKUP (a safety net) now skips a file deleted between listing and copy (`copyOptions.SkipVanished`), the move itself stays strict (a vanished file there still fails the move).

