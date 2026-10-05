# PLAT-513 — Code browser extension setup, clear status and stable connections

State: fixed on main, not deployed. Date: 2026-10-05. Priority: P2. Requested by owner.

## Problem and decision

The browser extension setup overwhelms the Browser pane with instructions and
raw connection JSON, offers two competing browser controls, and leaves its
popup form visible after connecting. Repeated pairing codes expire/change.
The owner requested a cohesive UI, a real icon, stable codes and a rollout
limited to Code projects; Workflows and Crews are deferred.

## Implemented

- The existing browser picker includes My Chrome or Edge · extension in Code
  only. Its compact settings contain download/unzip/Load unpacked instructions
  for both browsers, copy with manual fallback, and explicit reset.
- Selecting the extension gives the entire Browser pane its own connection
  state and shared tabs; workspace launch/teaching controls disappear. Selecting
  an ordinary browser disconnects the private binding first.
- The branded extension popup separates disconnected setup, connected with no
  shared tabs, and ready states. Share this tab, New shared tab, Group shared
  tabs and Disconnect browser are explicit actions. Groups organize already
  shared tabs; moving unrelated tabs into a group does not grant access.
- A reusable private code is bound to account + server-derived Code workspace,
  persisted with mode 0600. Copy/reconnect/server restart do not rotate it;
  Reset rotates it and closes live authority. The last connected browser wins.
- Connection and heartbeat checks enforce current Code product/workspace write
  access and enabled account state. Workflows/Crews cannot mint or use codes.
  Live authority remains process-local, explicit, limited to eight hours, and
  has one controlling root conversation. Reconnection gets fresh target refs.
- Restored Code selections remain disconnected and fail closed. No shared tabs,
  debugger authority or private relay capability is restored from disk.

## Verification

- Real Chrome-for-Testing 153.0.8010.12 and Microsoft Edge 154.0.4258.53:
  actual unpacked extension, agent-browser and guarded workspace shell passed
  snapshot/ref fill/click, screenshot artifacts, cookie retention, new tab,
  protected/unshared tab exclusion, endpoint override refusal, pinned closed
  tab refusal and immediate Stop with host CDP disabled. Popup form/badge/icon,
  shared-only groups, new-tab group membership, explicit disconnect and same-code
  reconnect without restored tab authority passed.
- Grouping qualification caught a real new-tab timing bug: before a pending
  URL arrived, a tab update removed the newly shared target. Accept initial
  about:blank/pending HTTP(S) updates while retaining protected-page rejection.
- Relay race check passed: stable/reusable codes, fresh reconnect session,
  private 0600 storage and restart persistence, legacy disconnected Code
  selection migration, active reset/revocation, account/workspace/controller/chat
  isolation, Stop and explicit disconnect.
- Focused server browser/auth checks passed, including owner-only Code pairing,
  stable copy, Workflow/Crew/wrong-profile denial and real WebSocket rejection
  after disabling the owner, both on heartbeat and a subsequent connection.
- Browser-rendered UI flow passed Code choice, concealed JSON, repeated stable
  copy, empty/ready connection states, new connection identity for Reconnect,
  explicit Reset and managed-browser switching. Workflow/Crew views neither
  offer nor query extension access. Dark/light and 1000/420px layouts passed
  with no horizontal overflow; screenshots visually inspected.
- Existing frontend browser/settings/guide tests: 21 passed. Production frontend
  build, Go server build and packaged ZIP/source/icon equality passed.
- UI HTTP responses are controlled. Actual browser transport/actions use the
  separate real extension E2E. No running user checkout was built or tested.

## Remaining

Deployment and Chrome/Edge store publication remain outside this change.
Workflows and Crews remain deferred by the owner.

[Design](../../../core/browser.md#personal-chrome-extension).
