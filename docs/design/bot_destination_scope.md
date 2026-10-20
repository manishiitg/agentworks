# Bot destinations: one canonical scope for crews

Status: shipped 2026-09-26 (phases 1–3 and Save & test).

## Problem

A crew project has two path forms:

| Form | Example | Who uses it |
|---|---|---|
| Logical | `Chats/Work/projects/<id>` | the crew UI; the user-scoped workspace API |
| Physical | `_users/<owner>/Chats/Work/projects/<id>` | crew turns, the server's own file reads, owner lookup |

Workflows have one form (`Workflow/<name>`), so bot code written for workflows
stored and compared paths as plain strings. Crews were added on top, and each
place picked a form. On 2026-09-25/26 that caused, one per deploy:

- a crew's own Slack bot selected with the logical path: "product manifest not found";
- its Slack route with no owner: "This Slack route is no longer configured";
- shared-bot crew channels refused: "does not match the selected product workspace";
- legacy crew bots locking their owner out (403), and a repair dropping their channels.

The server reads files without a user identity, so a logical path names no
folder there, and `routeWorkspaceUserID` reads the owner only from a
`_users/<id>/` prefix.

## Invariant

**A stored crew destination is always physical.** Every Slack store that holds
a crew path (the connection registry, a connection's channel routes, the
platform channel routes) holds `_users/<owner>/...`. The owner is part of the
path, and platform routes also carry `workspace_user_id`.

- **Edge:** every bot endpoint converts an incoming crew path once, with
  `canonicalBotScope` (a logical path names the caller's own crew; another
  owner's crew is always addressed physically).
- **Storage:** the Slack stores refuse a logical crew path
  (`services.ValidateBotScope`). A future caller that forgets the edge
  conversion fails loudly in tests instead of breaking in Slack.
- **Migration:** startup rewrites legacy logical crew paths to the one user
  tree that holds the project (`migrateCrewBotScopes`); zero or several
  candidates leave the record and log it.

Workflow paths are unaffected. WhatsApp keeps its per-user route store (its
checks already compare for that user).

## Turn requests

Every bot turn (workflow, crew conversation, default chat) gets its platform
fields from one helper: platform, channel, thread and arrival connection. The
crew-conversation builder once omitted the connection, so revalidation looked
in the shared bot's routes and refused a crew's own bot.

## Verification

Verify a route with a real Slack mention or direct message and inspect the resulting chat and server logs. Connection tests check the Slack app credentials; receiving a real message proves the full route.

## Tolerant comparisons

Before the invariant, several checks were taught to accept both forms
(`SameSlackScopePath`, `slackRouteFolderMatches`, `workspacePathsMatchForUser`
in route checks, the owner lookup in `dedicatedSlackProfileRoute`). With the
migration and the storage guard they are no longer load-bearing; they stay as
defense for records written by an older client during an upgrade. New code
relies on the invariant, not on them.
