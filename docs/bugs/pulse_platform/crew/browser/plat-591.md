[← crew / browser](index.md)

# PLAT-591: Start browser fails for a Crew moved to the shared root

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | crew |
| Area | browser |
| Summary | Browser Start uses a moved Crew’s retained legacy browser key as its working directory, causing Directory does not exist for Ashutosh’s yami Crew on Excellence. |

## What happened

Ashutosh's Excellence Crew `yami-57c29c55` is registered at `Crew/yami-57c29c55`.
Its legacy alias is
`_users/70ff7b0da57bee2bbf0c2e9719698e16/Chats/Work/projects/yami-57c29c55`.
Start returned `Directory does not exist` for that old folder.

`browserWorkspaceAccess` returned `browserProjectKey` as the physical execution
folder. A moved Crew deliberately retains its old browser key to keep its
profile, cookies and tabs. That key is an identity, not a directory that still
exists after migration.

## Fix

Resolve the authorized Crew path through `resolveCrewPath` for browser settings,
execution and folder grants. Continue using `browserProjectKey` to identify its
browser session, preserving the user's existing browser profile.

Verification: `RUN_BROWSER_TEACH_E2E=1 go test ./cmd/server -run
'^TestMovedCrewBrowserStartsRealChrome$' -count=1` starts real Chrome through the
real workspace shell handler with an absent legacy directory. Start succeeds
through both the current and old reference and returns the same browser session.
Restoring the old implementation reproduces the incorrect directory.
Existing browser authorization and moved-reference checks pass.

## Left

Deploy to Excellence and verify Ashutosh's next browser Start.
