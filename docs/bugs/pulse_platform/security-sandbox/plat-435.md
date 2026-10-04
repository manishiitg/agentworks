# PLAT-435 — One workspace path type: stop the `_users/<id>/` prefix bugs from coming back

Status: in progress (owner approved 2026-10-04; package built, migration under way). Opened 2026-10-04 after PLAT-434, the latest of a long series.

## Why it keeps coming back

The same folder exists in two spellings: *logical* (`Chats/Code/projects/p`, what clients and manifests use) and
*physical* (`_users/<id>/Chats/Code/projects/p`, what storage and the runtime use). Both are plain strings, and every
caller decides for itself how to compare or strip them. Counted on main 2026-10-04: 82 production Go files mention
`_users/`, 17 raw `HasPrefix`/`TrimPrefix` checks on it, and at least three normalisers that do not agree
(`normalizeConversationWorkspace` strips *any* user's prefix, `canonicalChatHistoryWorkspacePath` only the caller's,
`session_workspace.go` another variant). A new caller that picks the wrong one works on a single-user laptop and RTS and
breaks only on multi-user servers, which is why tests and local runs miss it. Earlier instances: PLAT-170 (presentation
tools got canonical paths), crew Slack apps and bot destinations stored with the wrong form (commits 31b0734b8,
a7eff2376, e4937721d, 3363fefc5), PLAT-324 (tabs lost on reload), the 2026-09 secrets path reversal in DECISIONS.

## Proposal (needs the owner's go-ahead; touches many files)

1. One type `WorkspaceRef{User, Logical}` built by one `ParseWorkspace(path)` (accepts both spellings; keeps the owner
   when the prefix is present) with `.Logical()`, `.Physical(user)`, `.Owner()`, `.SameAs(other)`, `.IsProject()`,
   `.Product()`. No other code strips or tests `_users/`.
2. Replace the 17 raw checks and the three normalisers with it, one package at a time, each with the two-spelling test.
3. A guard test that fails when production code outside that package contains `"_users/"` string handling, so a new
   caller cannot reintroduce it. Run every path-sensitive test under both spellings (a shared test helper).
4. Run the e2e suites on a multi-user fixture, not only the single-user one.

## Done so far

PLAT-434 fixed the Code/Crew UI-control instance.

## Progress (2026-10-04)

- `agent_go/pkg/workspaceref` built: `Ref`, `Parse`, `MustParse`, `Logical()` (strips ANY owner: which product/project),
  `SameFor(user, other)` / `SameAs` / `OwnedBy` / `OwnedByOrUnowned` (identity for access), `Physical(user)`,
  `PhysicalKeepOwner(user)`, `Project()` / `IsProject()` over the single `ProjectRoots` table, `SanitizeUserID` (one
  implementation). Table tests for every spelling; `reftest.BothSpellings` shared helper.
- Migrated (commit 2): `normalizeConversationWorkspace` (Logical), `canonicalChatHistoryWorkspacePath` /
  `pkg/common.CanonicalSessionWorkspace` (both `workspaceref.CanonicalFor`), `workspacePathsMatchForUser` (`SameFor`),
  `projectProductForPath` (`Ref.Project`; single project-root table, `codeproduct.ProjectsRoot` takes it from there),
  `sanitizeUserIDForPath` + `pkg/common` + `pkg/chathistory` sanitizers (`SanitizeUserID`), `ClassifySessionWorkspace`,
  `CodeProjectRoot`.
- Pre-existing failures on main 0cf79e4cf, unrelated: TestPrivateCodeCallerIsSeparateFromCrewWithSameProjectID,
  TestSalesCrewCatalogHasInstallableRoles, TestCrewProductSurfaceE2E (native-subagents undeclared).
- Migrated (commit 3, cmd/server): `cleanAgentProfileWorkspace`, `productConversationRuntimeWorkspace`,
  `crewProjectOwnerID` / `isCrewProjectPath` / `crewProjectOwnedByCaller`, place MCP roots (`cleanAttachRoot`,
  `isCodePlaceRoot`, `placeRootOf`, `attachRootForCaller`, `placeMCPCanAttach`), `command_routes` shape check,
  `ui_control_routes` ownership, `workspaceProxyPathIsOtherUser`, `chat_history_persistence` (restored-path check,
  legacy owner check, `ownedWorkProjectWorkspacePath`). Tests: `workspaceref_sites_test.go` (both spellings + another
  user's physical path). `Parse` counts a mid-path `_users` segment as a prefix only for absolute paths (a relative
  `Chats/x/_users/bob/y` is an ordinary folder name; the old code stripped it).
- Found while migrating: `crewProjectOwnedByCaller` treated an absolute doc-root path of ANOTHER user's crew as the
  caller's own (the `_users/` prefix test missed it); `crewProjectOwnerID` read the owner from an uncleaned
  `_users/alice/../bob/...`. Both now go through `Parse`.
- Also failing on main, unrelated: TestGetRelayCommandCatalogWithoutGenericRuntimeRegistration.
- Migrated (commit 4, cmd/server/services): `routeWorkspaceUserID`, `SameSlackScopePath` (owner-agnostic only when one
  side is logical, as before), `physicalBotScopeOwner`; test `services/workspaceref_sites_test.go`.
- Migrated (commit 5): `sparkquillproduct.runtimeRoot`, `videoproduct.profileWorkspaceRoot` (an escaping `..` path now
  maps to a non-existent folder, not to the user's whole tree; old code joined it, so `../bob/x` reached `_users/bob/x`),
  `pkg/browser.captureWorkspace`, `livefeed.IsPlanPath`, `presentations.workspaceDatabasePath`. Tests in each package.
- Migrated (commit 6, cmd/server parsers and builders): `externalIsCrewRoot`, `parseCrewPath`, `isOtherOwnerCrewPath`,
  shared-asset parsing (`shared_assets.go`, `shared_assets_crew.go`), `codeFilesDeletionProtected`, `browserProjectKey`,
  `costOverviewRoot`/`costOverviewIsCode`, `isChatsWriteFolder` (a relative `x/_users/y/chats` no longer counts: only an
  absolute path carries a document root), `workspaceReadAllowed`, `workspaceGitWriteAllowed`, code-peer root check;
  physical-path builders (`PhysicalPath`/`PhysicalPathOf`) in agent_profile_routes, chat_submission_journal,
  crew_functions, code_peer_functions, slack_trigger, chat_history_persistence, command_routes, custom_command_tools,
  browser_live, workspace_git, instructions. Tests added to `workspaceref_sites_test.go`.
