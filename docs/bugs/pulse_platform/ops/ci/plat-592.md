[← ops / ci](index.md)

# PLAT-592: Go tests failing on main

| Field | Value |
|---|---|
| State | open |
| Priority | P2 |
| Product | ops |
| Area | ci |
| Summary | About 20 Go tests fail on clean main (provider accounts, playbooks, relay catalog, Code prompt, terminal, Slack tools, and others) |

## What happened

## Fix

## Left

## What

Found 2026-10-06 while checking PLAT-576: on a clean `origin/main` (after `676717eb3`), `go test ./cmd/server/ ./pkg/... ./internal/...` fails these (macOS, local):

TestCodePreparedSystemPrompt, TestCodeSkillOptionsStayPrivateAndRefreshWithoutMutatingBuiltins, TestExternalMCPStreamableSpecAndCall, TestGetRelayCommandCatalogWithoutGenericRuntimeRegistration, TestLoadPlaybookCatalogFindsEngineeringPlaybooks, TestNativeTerminalRealTmuxKeyboardAndPaste, TestNoUsersPathHandlingOutsideWorkspaceref, TestPrivateCodeCallerIsSeparateFromCrewWithSameProjectID, TestProviderAccountsServerSignInAdminOnly, TestProviderAccountsUsageForNonManagersIsNotInteractive, TestProviderAccountsUsageRunsInAccountHome, TestResolveDelegationTierConfigExpandsProviderProfile, TestResolveProviderProfileConfigUsesBuilderDefaults, TestSalesCrewCatalogHasInstallableRoles, TestSearchPlaybooksReturnsWebsiteGrowthTeamProposal, TestSlackToolsFollowSessionOriginAndReadOnlyPolicy, TestTerminalToChatPageRecoversPrivateClaudeTurnsOnce, TestUserAccessToolsAbsentOutsideBuilder, TestValidateStepLLMConfigEnforcesAgyAlphaGate, TestVaultChatCreatesMissingUserWorkspaceBeforeCLILaunch, TestWorkshopResolveLLMConfigExpandsCodingAgentMode.

`TestProtectManagedCodingAgentProjectionWritesPreservesExistingDenies` was also failing; it pinned the whole-folder projection guard and is fixed with PLAT-576 (it now pins the PLAT-568 rule). `TestHangDiagnosticsTellsBusyPageFromHealthyPage` (pkg/browser) failed once and looks timing-dependent.

## Left

Triage each: stale test after a behaviour change, environment-dependent (needs a CLI, tmux, network), or a real regression. Not investigated.
