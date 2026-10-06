[← platform / frontend-chat](index.md)

# PLAT-551 — Product workspace toolbar views reset after page refresh

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | app |
| Area | ui |
| Summary | fixed on main, not deployed. |

| Coordination | Value |
|---|---|
| State | fixed on main; not deployed |
| Date | 2026-10-05 |
| Owner | frontend-chat |

## Report and cause

The owner reported that selecting a view inside Goals, then refreshing the page,
did not remember that view, and requested the same shared code across products.
Goals already saved a per-workflow choice, but layout hydration could select and
persist Files before the workflow restored its view. Several other products used
component defaults without persistent selection. Code and Crew had their own
unscoped persistence path.

## Done

- One shared storage helper validates and saves workspace-view preferences by
  server connection, product and project/workflow (or product-wide workspace).
  Existing Goals/Relays and Code/Crew preferences migrate on first read.
- Goals and Relays restore their own view without resetting execution state when
  switching products. Files-layout synchronization waits for workflow restoration;
  subsequent explicit Files toggles retain their existing behavior.
- Code, Crew, Brain, Vault, VideoStudio, Dominion and SparkQuill use the shared
  preference code. Component-based products share one restoration hook; existing
  workflow and SparkQuill stores use the same underlying storage helper.
- Vault remembers both its workspace panel and its Audit/Connect destination;
  explicit navigation requests still take precedence. Disabled/shared Code/Crew
  panels retain existing access gating. Product defaults never write a preference
  merely because data is loading.
- This concerns product workspace toolbar views. Global Providers, Activity and
  admin-page navigation retain their existing transient behavior.

## Verification

- 52 frontend tests across seven files passed. Two focused regressions cover all
  product preference remounts, project/server isolation, legacy migration, delayed
  Goals restoration and preserving execution state across Goals/Relays.
- TypeScript passed. Targeted lint passed with the pre-existing WorkflowLayout
  dependency warning only.
- An isolated browser fixture using production persistence hooks, toolbar controls,
  Goals store and layout synchronization retained all nine selected destinations
  after a full page reload, including delayed Goals hydration. This checks the
  shared path; it does not claim a live deployed check of every product/backend.

## Left

Release the frontend and verify the owner's instance after deployment.

## Register notes

[PLAT-551](plat-551.md), P2, fixed on main, not deployed. Shared server/product/project view preferences across all nine products; delay Goals Files synchronization until its saved view restores. Regression checks and an isolated full-page browser reload passed.
