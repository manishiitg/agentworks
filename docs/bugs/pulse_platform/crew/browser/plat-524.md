# PLAT-524 — Account browser token and simultaneous Code/Crew connections

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | crew |
| Area | browser |
| Summary | fixed on main, not deployed. |

State: fixed on main, not deployed. Date: 2026-10-05. Priority: P2. Requested by owner.

## Decision and implementation

Use one persistent private token per account per deployment across owned Code
and Crew projects. Keep project routing, controller ownership, shared targets,
CDP capabilities and tab groups separate. Workflows remain outside this rollout.

- The authenticated management API registers a server-derived Code/Crew project
  grant and returns the shared account token plus routing scope. Crew uses the
  existing `work` profile. Product/write/ownership checks apply on management,
  socket connection and every heartbeat; readers cannot pair the owner's browser.
- The worker holds multiple project connections in memory. Paste once, then use
  Copy connection in another owned project to register it; the server asks the
  paired worker to connect it automatically. The project picker is optional
  and only determines where a manually shared tab goes. Each project may
  connect simultaneously, with separate tabs/groups. One tab can belong to only
  one project; dragging it between groups does not transfer authorization.
- Empty projects support agent `open` and `tab new` without a manual first share.
  A bounded worker request creates a scoped target before CLI bootstrap; first-tab
  native labels use a temporary target that is removed after CLI creation.
- Stopping one project preserves other project connections. Account Reset
  rotates the token and closes every account binding. Another browser replaces
  only the project it connects to. Scope metadata cannot authorize an invented
  or another account's project. Missing scope is accepted only for an unambiguous
  single-project credential or an original legacy project credential.
- Upgrade an existing project code to the account credential without rotating
  that chosen token; other legacy project copies retain their original scope
  until account Reset. Newly copied codes use the canonical account token.
  Persist credentials in the existing 0600 private file; selections contain no
  credential. Restart restores disconnected Code/Crew selections, not authority.
- Crew browser UI and global chat notifications follow the existing Code flow.
  Code's explicit browser choices and Crew's Automatic behavior remain unchanged.
- Extension package version 0.3.0 and canonical browser design describe this model.

## Verification

- Real Chrome 153.0.8010.12 and Edge 154.0.4258.53 checks cover simultaneous
  Code/Crew sockets, separate groups/target lists, refusal to double-share a tab,
  automatic project connection and agent-created first tabs without manual sharing,
  native first-tab labels, background operations, snapshots/fill/click, screenshots,
  cookie retention, immediate stop and project-local disconnect.
- Live WebSocket relay checks pass under the race detector for stable account codes,
  distinct capabilities/controllers, unknown/ambiguous scope rejection, restart
  persistence and account Reset revoking all project sockets.
- Real management/connection/heartbeat handlers check Code/Crew tokens and scopes,
  owner-only Crew access (including registered shared-root Crew) and disabled users.
- Browser-rendered UI/global-queue checks pass for Code/Crew availability, connection,
  ready/disconnect notices, stable copy/reset, reconnect, draft preservation and
  dark/light/narrow layouts. Three focused notification checks and the production
  frontend build pass. ZIP/source equality and JavaScript syntax checks pass.
- Original review issues were verified by source only; no live exploit or runtime
  fixes to direct-CDP reuse, global eviction or force cleanup are claimed.

## Remaining

Rebuild/deploy the application and reload the unpacked extension. Existing copies
should be recopied to include project scope and the canonical account token.
The account token is private; owning another project does not expose a different
user's browser, and the direct-CDP review issues PLAT-520–523 remain open.

## Register notes

[PLAT-524](plat-524.md), P2, fixed on main, not deployed. Persistent account token, separately authorized concurrent projects, Crew rollout and project picker.
