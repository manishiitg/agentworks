[← platform / security-sandbox](index.md)

# PLAT-440 — Physical `_users/<id>/` paths built from an unsanitized or differently sanitized user id

| Field | Value |
|---|---|
| State | open |
| Priority | - |
| Product | sandbox |
| Area | security |
| Summary | open: found during PLAT-435; a few call sites build `_users/<id>/` from a raw or differently sanitized user id, and the workspace module's default-user fallback differs from the agent server's. |

| Coordination | Value |
|---|---|
| State | open (found while doing PLAT-435; not fixed) |
| Date | 2026-10-04 |
| Owner | security-sandbox |

## Finding

`workspaceref.SanitizeUserID` (and `sanitizeUserIDForPath`, the `pkg/common` and `pkg/chathistory` copies it replaced)
maps an empty, over-long or odd user id to `"default"`. Several places build a physical path from a user id without
that rule, or with a different one, so for an id outside `[a-zA-Z0-9_-]` they name a folder the rest of the system
never uses:

- `cmd/server/multiagent_config_store.go` `multiAgentConfigPath`, `services/workspace_config.go`
  `LoadMultiAgentChatCapabilities`, `services/bot_connector.go` `loadRecentChatTurns`, `pkg/workspace/client.go`
  folder-guard rewrite: raw `userID`.
- `services/whatsapp_service.go` and `services/slack_service.go` upload folders: `sanitizeWhatsAppFileName`, a
  file-name sanitizer, not the path rule.
- `workspace/utils.SanitizeUserID` (the workspace module) falls back to `GetDefaultUserID()` (the `DEFAULT_USER_ID`
  env var), the agent server falls back to the literal `"default"`: the two disagree whenever `DEFAULT_USER_ID` is set.

Normal accounts have valid ids, so nothing is known to break today; the risk is the same class as PLAT-435 (works on
the common path, wrong on an unusual one).

## Left

Decide the rule (one sanitizer, one default), then switch these call sites from `workspaceref.PhysicalPathOf` (marks
"owner not sanitized"; `grep PhysicalPathOf`) to `workspaceref.PhysicalPath`, with a test using an odd id. Align the
workspace module's fallback.

## Register notes

[PLAT-440](plat-440.md), open: found during PLAT-435; a few call sites build
`_users/<id>/` from a raw or differently sanitized user id, and the workspace module's default-user fallback differs
from the agent server's.
