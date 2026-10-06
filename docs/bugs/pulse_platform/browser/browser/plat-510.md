# PLAT-510 — Connect personal Chrome through an extension CDP bridge

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | browser |
| Area | browser |
| Summary | built on main, not deployed. |

State: built on main, not deployed or published to the Chrome Web Store.
Date: 2026-10-05. Priority: P2. Requested by owner.

## Problem and decision

Hosted agents can use managed server Chrome but cannot connect to explicitly
shared tabs in a user's existing local Chrome through an extension. Retain
agent-browser and the existing browser tool, with an authenticated extension/CDP
adapter. [Design](../../../../core/browser.md#personal-chrome-extension).

## Current scope

[PLAT-513](plat-513.md) supersedes the original five-minute single-use pairing
and broad rollout below: Code only, reusable private codes with explicit Reset,
and integrated browser settings/connected popup. Original qualification here
records the implementation as it was tested at the time.

## Implemented

- Manifest V3 extension using Chrome 125+ debugger/tabs, explicit sharing,
  scoped tab creation, Stop and a restricted CDP adapter. No remote debugging
  port or CLI runs on the user's laptop.
- Account/workspace-scoped, five-minute single-use pairing and eight-hour live
  connections. Private capability-protected relay; authenticated management
  routes and a narrow extension WebSocket gateway exception.
- Backend-owned tool routing through the real guarded workspace shell, one
  controlling chat/run per binding, pinned tab identity and screenshot artifact
  transfer. Stop, lost connections and restarts fail closed; selected browser
  state survives without credentials. App disconnect restores workspace Chrome.
- App pairing/status/reconnect controls, downloadable embedded extension ZIP,
  deterministic packager and [installation instructions](../../../../../extensions/README.md).

## Verification, 2026-10-05

- Real Chrome-for-Testing 153.0.8010.12, unpacked extension and agent-browser
  0.38.2 through the actual tool/workspace execution path: snapshot, reference
  fill/click, screenshot artifact, navigation, existing cookie retention, scoped
  tab creation, unshared-tab exclusion, protected-page refusal, endpoint override
  refusal, pinned closed-tab refusal and immediate Stop. Passed with operator
  host CDP disabled, proving this independent routing path.
- Real WebSocket relay test under the race detector: wrong account/scope,
  expired/reused pairing, wrong capability, second CDP controller, second chat,
  restart selection without credentials, Stop and explicit disconnect. Passed.
- Full browser package, relevant server authentication/workspace-browser tests,
  gateway tests and Go CLI build passed. Browser/debug-log handler checks passed
  excluding the separately reproduced baseline fixture failure [PLAT-511](plat-511.md).
- Full production frontend build and existing browser-panel/settings tests
  passed. UI connection/disconnect checks at 1000px and 420px passed without
  overflow; narrow pairing screenshot visually inspected.
- Full workspace handler suite has the existing capture fixture failure tracked
  in PLAT-511; reproduced after restoring the unchanged tracker in this owned
  worktree. It is not evidence of a Chrome bridge regression.

## Local requalification of main, 2026-10-05

The implementation commit `dc05cd0e7` is already contained in main. Tested main
at `832f0adf5` in a new owned worktree, using temporary browser profiles and the
real guarded workspace shell; the user's running checkout was not used for
builds or tests.

- Chrome-for-Testing 153.0.8010.12: the complete extension end-to-end check passed
  again, including actions, screenshot transfer, cookies, tab isolation, pinned
  closed-tab refusal and Stop with host CDP disabled.
- Microsoft Edge desktop 154.0.4258.53: the same unpacked package and complete
  end-to-end check passed. No Google login or store installation was used.
- Relay race test, focused server authentication/workspace-browser checks,
  gateway tests and four existing frontend browser-panel/settings tests passed.
- Browser-rendered pairing, responsive controls at 1000px/420px and explicit
  disconnect passed; the narrow pairing screenshot was visually inspected.

These checks do not qualify console/network inspection, the public deployment's
WSS gateway, or every older Edge version. Production deployment remains pending.

## Remaining release scope

Deploy/restart the platform, then install the unpacked ZIP and qualify the actual
public WSS gateway in the target deployment. Chrome Web Store publication is a
separate release action. Teaching, local upload/download transfer, recording,
HAR, native dialogs and full CDP compatibility are not included. Laptop/Chrome
must remain awake and connected. No production deployment was performed.

## Register notes

[PLAT-510](plat-510.md), P2, built on main, not deployed. Private extension/CDP bridge retaining agent-browser, verified with real Chrome and guarded workspace execution.
