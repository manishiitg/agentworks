# PLAT-532 — Remember browser extension connections when users return

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | browser |
| Area | browser |
| Summary | fixed on main, not deployed: extension 0.4.0 remembers enabled project connections, resumes after transient loss/restarts and respects offline Disconnect; browser restart clears tab grants. |

State: fixed on main, not deployed. Date: 2026-10-05. Priority: P2. Requested by owner.

## Cause and behavior

The account token was stable on the server, but the extension held pairings only
in worker memory and never retried a lost socket. Returning after network loss,
sleep or a restart therefore looked disconnected until another manual paste.

- Extension 0.4.0 remembers explicitly enabled account/workspace pairings in
  chrome.storage.local, restricted to trusted extension contexts, never sync.
  Saved selection and project labels return with the pairing. Bounded retry and
  alarm wake-ups reconnect after transient loss, worker/server/browser restarts
  or the existing eight-hour socket expiry. Offline popup says Reconnecting,
  keeps Disconnect available and disables tab actions until live pairing succeeds.
- Each reconnect authenticates again and receives a fresh relay capability,
  controller and reference session. Stable account tokens and workspace isolation
  remain unchanged; no fallback browser or foreground activation occurs.
- Session-only storage retains exact explicitly shared tab IDs across transient
  loss in the same browser session. Resume never adopts the active page, matches
  URLs/titles/groups or detaches another project's tab. Browser restart/extension
  reload clears session grants: Connected with zero tabs, ready for the agent to
  create its own first tab without another pairing or manual Share.
- Popup Disconnect forgets its pairing before stopping; Disconnect all forgets
  all pairings. Server Disconnect/Reset/access revocation/replacement send a
  non-retryable close reason. Resume also requires the durable server selection,
  so an app Disconnect while the browser is offline cannot be undone on return.
  Explicit Connect can enable a project again. Account Reset still rotates tokens.

## Verification

Real Chrome 153.0.8010.12 and Edge 154.0.4258.53 through guarded workspace shell,
native agent-browser, persistent relay and unpacked extension: server restart
renews the connection and restores only explicitly shared session IDs; an actual
browser close/relaunch with the same isolated profile reconnects without paste,
restores zero tab grants and permits agent-created tabs. App Disconnect while
that browser is offline survives relaunch; account Reset clears remembered
pairings. Existing Code/Crew/workflow steps, target/group isolation, login cookie,
snapshot/fill/click, screenshot transfer and background/focus checks pass.

Relay race checks pass, including the replacement close-code regression. Source
syntax, package/source equality and diff checks pass. No actual model, scheduled
job or user's existing browser profile was used. Long-duration sleep and actual
eight-hour waiting were not exercised; they use the same tested reconnect path.

## Remaining

Rebuild/deploy the platform and load/reload extension 0.4.0, then connect once to
seed local remembered state. Existing unpacked extension installations do not
receive source/permission changes automatically. Relay rollout and existing
open direct-CDP security tickets remain outside this change.

## Register notes

[PLAT-532](plat-532.md), fixed on main, not deployed: extension 0.4.0 remembers enabled project connections, resumes after transient loss/restarts and respects offline Disconnect; browser restart clears tab grants.
