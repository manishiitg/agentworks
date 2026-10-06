[← app / tests](index.md)

# PLAT-626: Record existing main CI failures outside Relay landing regression

| Field | Value |
|---|---|
| State | open |
| Priority | P2 |
| Product | app |
| Area | tests |
| Summary | Current main has schedule/help/view/Vault test failures and undeclared authenticated-user prompt sections, separately from the fixed Relay landing assertion. |

## What happened

Audit of `dc4669ae0` from an isolated worktree, after fixing the Relay landing
assertion ([PLAT-627](../../relays/frontend-chat/plat-627.md)):

- `SchedulesPage.test.tsx`: four Back navigation tests expect
  `setShowWorkflowsOverview(false)`; the schedule component was unchanged by
  the Python Relay commit.
- `WorkspacePanelGuideButton.test.tsx`: two Skills help assertions expect the
  former answer count and “Skills for this project” text. This component was
  unchanged by the Relay commit.
- `workspaceToolbarPlacement.test.ts`: one assertion expects the old literal
  `loadLegacyWorkspaceViewByPreset()[presetId] ?? 'report'`, changed in PLAT-551
  (`fea0bc976`), before the Python Relay change. PLAT-577 did not touch it.
- `GatewaySurface.test.tsx`: fourteen failures reading
  `state.activeSessionsCache.find` in `ChatTabPill`. The shared tab status change
  is separate from the Relay patch; the Vault test store lacks that array.
- `TestAgentWorksProductSurfaceE2E` and `TestCrewProductSurfaceE2E` fail because
  the shared `authenticated-user` section is absent from their declarations.
  That section was added by `9b345dc75`, not by PLAT-611.

Full frontend run: 4 failed / 504 passed / 1 skipped files; 21 failed / 2866
passed / 1 skipped tests. Both product declaration failures reproduced locally.
Latest failed CI evidence:
[Frontend CI](https://github.com/manishiitg/agentworks/actions/runs/37469293749),
[Coding CLI P0](https://github.com/manishiitg/agentworks/actions/runs/37469692051).
The frontend CI run precedes the new Vault tab-status failures and also includes
the Relay assertion now fixed in PLAT-627.

P0 on the parent of PLAT-611 was already failing on the sandbox/secrets generated
index; the PLAT-611 run had the same index failure. Current ticket checks pass.
[Parent P0](https://github.com/manishiitg/agentworks/actions/runs/37460044580).

## Fix

None in this audit: the requested scope was failures caused by the Relay change.
Existing frontend CI cleanup was previously recorded under PLAT-481; PLAT-571
also notes the stale workflow assertions, but neither is a current source of
truth for this complete failure list.

## Left

Repair the affected fixtures/contracts after confirming their intended product
behavior, run the named suites, then rerun frontend and P0 CI. Do not describe
main as globally green until those checks pass.
