[← relays / frontend-chat](index.md)

# PLAT-378 — Relay commands and private system prompt inspection

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | relays |
| Area | frontend-chat |
| Summary | implemented and locally verified; deployment pending. |

| Coordination | Value |
|---|---|
| State | implemented and verified locally; deployment pending |
| Date | 2026-10-03 |
| Owner | frontend-chat |

## Request / cause

Relays inherited the shared Workflow composer’s unconditional AgentWorks command
catalog, so it offered dashboards, goals, Pulse and report publishing. Its builder
already uses the Relay `product.yaml`, but that boundary was missing in the menu.
Generated CLI instructions live in the private per-chat runtime, outside the
workspace. Showing hidden workspace files therefore could not reveal them.

## Implemented

- Define six Relay commands in its existing `product.yaml`: `design-graph`,
  `test`, `publish`, `versions`, `setup-api`, `schedule`. Publish calls
  `publish_relay` for an API version. Schedule explicitly describes draft runs.
- Reuse the existing product manifest loader, metadata endpoint, command adapter,
  custom commands and picker. Relay metadata does not register a generic profile
  execution path. Product enablement and account access still apply.
- Select the catalog by product; clear it on switches and ignore late responses.
  Typed commands and picker selections use the same product/custom-command lookup.
  Relay excludes Workflow Pulse commands. Its builder can manage custom commands
  through the existing tool, declared in the Relay product allowlist and skill.
- Under the existing local/admin hidden-file visibility policy, Workspace shows a
  virtual `AGENTS.md` entry for the selected chat. It opens a read-only view of
  the last finalized prompt, for CLI and API providers. No project file is created,
  replaced or edited, and no private runtime directory is listed.
- Snapshots are stored atomically in private server state (0700 directories,
  0600 files), keyed by authenticated owner and session hashes. The endpoint cannot
  select another user’s snapshot, rechecks current workspace access, and forbids
  caching. Snapshot state survives a restart and is removed on chat clear/delete.
- Existing chats need one new message after deployment to capture their prompt.
  The viewer explains this when no snapshot exists.

## Verification / remaining

- 41 targeted frontend tests passed (catalog selection/races, real composer
  picker/typed publish, read-only prompt and existing picker regressions).
- Three existing Workspace bulk-delete tests, TypeScript and production build passed.
- Go Relay manifest, catalog route, snapshot privacy/restart/update/delete,
  finalized Code prompt capture, durable-chat and clear/delete regressions passed.
- In-app browser on isolated port 5194 verified the actual command picker and
  Workspace hidden toggle/prompt modal. Fixture APIs used product source data;
  finalized runtime capture was verified by the backend test. No running user
  backend or deployed service was changed.
- Remaining: deploy to Excellence, then send a message in an existing Relay chat
  before inspecting its captured prompt.

## Register notes

[PLAT-378](plat-378.md), implemented and locally
verified; deployment pending. Relay commands come from its product manifest;
hidden files offers an owner-private, read-only finalized prompt snapshot.
