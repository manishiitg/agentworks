# Slack Connections (Per-Workflow and Per-Project Slack Apps)

Each workflow either has its own Slack bot (a Slack app with its own bot
token + app token pair), uses one of its owner's other bots, or uses the
shared bot (the platform default app). Crew projects work the same way. This document describes who answers,
the registry, the multi-listener runtime, and the ownership rules.

## Who answers: three options

The Slack tab asks "Who answers for this workflow in Slack?":

| | Its own bot | One of my bots | Shared bot |
|---|---|---|---|
| Which app | A connection scoped to this workflow or crew project (`workspace_path` set) | A connection scoped to another workflow or crew the user manages | The default connection, or any unscoped one |
| Which workflow runs | Its own, in any channel it is invited to | This one in the channels picked here; the bot's own workflow everywhere else | The channel route (channel ID → workflow/project) |
| Channel routes | None needed | Stored on that bot (`channel_routes`) | Stored in the shared bot config (`allowed_channels`) |
| Managed by | The workflow/project owner | The bot's manager, who must also be able to write this workflow | Admin (Access → Slack) for the app; owners add channels |
| Admin needed | No | No | Yes, to set the bot up |

"One of my bots" exists because one Slack app can only be connected once
(`slackTokensInUse`): to answer for workflow B with the app workflow A
already owns, the owner adds channel routes to A's bot instead of
connecting the app twice.

- Inbound rule (`services.ResolveSlackRoute(ctx, connection, channel, …)`):
  a message's arrival app decides. A dedicated (scoped) app answers for the
  destination its own `channel_routes` names for the channel, else for its
  own destination (`StreamingAPI.dedicatedSlackRoute`, Run mode). A shared
  app follows the platform channel route. A dedicated app whose
  destination (own or routed) no longer resolves gets the revoked sentinel
  and refuses loudly; it never falls back to another workflow.
- A channel lists one or more targets per app (see "Slugs"). Another app in
  the same channel keeps its own rule.
- Crew bots: the owner comes from the project's `_users/<id>/` path and the
  conversation key from its `product.json`, re-checked through
  `resolveProductConversationBinding` like a saved route.
- The connection's scope is the grant: only the destination's owner (or an
  admin) can create a scoped connection. A dedicated app authorizes its
  own traffic like a saved route, independent of the platform switch.
- The same rule runs everywhere a route is re-checked: inbound routing,
  `revalidateExecutionPrincipal` (every tool call), `send_slack_message`
  and the Slack CLI tool (`slackRouteForConnection` / `slackToolRoute`).
  Non-bot sessions (a workflow run posting to a channel) only ever use
  channel routes.
- Sessions are per (thread, app): `ThreadID.Key()` appends `#<connection>`
  for non-default connections, so two bots in one thread keep separate
  conversations and each replies as itself. Default-connection keys and
  durable bindings keep the legacy three-part form; a non-default thread
  falls back to its legacy binding when the route key matches.
- Several bots in one thread: a plain (untagged) reply reaches none of
  them (`threadHasOtherBot`, in memory and via durable bindings); users
  @mention the bot they mean. A message tagging another bot stays silent
  on this bot (the colleague-tag guard).

## Slugs: one bot, many targets (PLAT-668)

Design: [docs/design/slack_slugs.md](../design/slack_slugs.md). One Slack app reaches many workflows, Crews and Codes;
a slug (the target's name, lowercase `[a-z0-9-]`, editable) picks one.

- **The AgentWorks bot** is the platform (unscoped) connection. A target is reachable from it only after its owner
  turns on "Use the AgentWorks bot" (`config/slack-targets.json` → `targets[].platform_bot`); an admin may limit the
  products (`products`). A target that already had the admin's channel route counts as on until its owner decides.
- **Channels** list allowed targets plus an optional default: on the platform bot, the admin's route
  (`allowed_channels`, the default when present) plus owner-added `channels[<id>].targets` (added only by someone Slack
  says is a member of the channel, `conversations.members`); on an own bot, `channel_routes[<id>]` whose destination
  is the default and whose `targets` are the others. A channel an own bot never listed answers for its own target.
  Routes saved before slugs read as a one-target list that is also the default.
- **Picking**: `@bot <slug> …` picks a target only when the word is on the channel's list (otherwise it is message
  text). The thread is bound (`config/slack-threads/<hash>.target.json`); a different slug there is refused. No slug:
  the default answers; no default: one button per allowed target, and the click binds the thread and runs the waiting
  message (`services/slack_slug_routing.go`).
- **DMs** (platform bot and own bots): the sender is matched by Slack email to one account; `<slug> …` picks among the
  targets they can reach with their own access (`slackCanReach`: workflow access, a Crew's owner or shared-with, a
  Code's owner only), remembered for the DM; `list` lists them. An own bot's DM targets are its own, its channel
  targets and `targets` (DM attachments, Codes included).
- **Triggers** run on an own bot's channel route too (`channel_routes[<id>].trigger`, for the channel's default
  target; set with the route tools' `own_bot: true` or `PUT /connections/{id}/channel-routes/{channel}` with
  `trigger`): the event's arrival app picks the route (`slackTriggerRoute`). Files attached to a top-level channel
  mention are downloaded like thread replies and DMs (`slackMentionFiles`).
- **Re-checks**: `ResolveSlackThreadRoute` (every message and follow-up), `slackRouteForTurn` in
  `revalidateExecutionPrincipal` and the Slack tools (the turn's target must still be on the channel's list and
  switched on), `slackDMRouteMatching` for DM turns. Codes never answer in channels.

## Model

```text
slack-config.json
  connections[]: [{ id, display_name, bot_token, app_token,
                    enabled, workspace_path, profile_id,
                    channel_routes: { "C123": { workspace_path,
                                                profile_id, added_by,
                                                targets: [ … ] } },
                    targets: [ { workspace_path, profile_id } ] }]
  default_connection_id: "slack_001"

slack-targets.json (PLAT-668)
  targets[]: [{ workspace_path, profile_id, slug, label, owner_id, platform_bot }]
  channels: { "C123": { targets: [ … ], default: { … } } }
  products: [] (= all) | ["workflows", "crew", "code"]

workflow.json capabilities (workflows)
  slack_connection_id: "slack_abc123" | "" (= inherit default)

project runtime manifest capabilities (crew projects)
  slack_connection_id: "slack_abc123" | "" (= inherit default)
```

- One connection = one Slack app = one Socket Mode websocket. Tokens are
  encrypted at rest (`operator:slack` AAD) and masked on every read.
- `workspace_path` scopes a connection to its owning workflow or product
  project (`profile_id` names the agent profile for product scopes).
  Empty means platform-managed. A lone scoped connection never
  auto-becomes the default; inheriting platform traffic is an explicit
  admin decision.
- `channel_routes` only applies to scoped connections. Normalization drops
  routes on unscoped connections, routes with no destination, and routes
  to the connection's own destination. Changing a connection's scope
  (admin only) clears its routes: they were granted by the old scope's
  owner.
- Pre-registry single-credential configs migrate on load into an unscoped
  `slack_001` "Default" connection. Legacy save shapes fold onto the
  default connection.
- An unknown, incomplete, or disabled selection fails the send. It never
  falls back to another identity: delivering from an unintended Slack app
  is worse than not delivering.

## Runtime

```text
Socket Mode listeners (one per enabled connection)
        |  inbound tagged with connection ID
        v
SlackService root (serves default) + children (one per extra connection)
        |  BotConnector / NotificationConnector
        v
BotConversationManager (connection rides ThreadID / BotIncomingMessage)
```

- The root instance (returned by `GetSlackService`, registered with the
  `BotConversationManager`) serves the default connection itself and fans
  out to one child `SlackService` per additional enabled connection.
- Children are owned by the root: created, token-synced, and stopped by
  `reconcileChildren`, which runs on every config reload. Children never
  touch the config file.
- `ThreadID.ConnectionID` (part of `Key()` for non-default connections;
  see "Who answers") carries the arrival connection through sessions,
  replies, reactions, button interactions, and history reads. Reactions and channel names resolve through
  the optional `connectionScopedConnector` interface; other platforms keep
  the legacy channel-only behavior.
- Resolution order for a send: live bot execution's arrival connection,
  else the route target's selection (workflow manifest or project runtime
  manifest), else the platform default.

## Ownership

| Action | Who |
|---|---|
| Create/update/delete/test a workflow-scoped connection | Owner of that workflow (create requires the scope; scope changes are admin-only) |
| Create/update/delete/test a project-scoped connection | Owner of that product project (same scope rules as workflows) |
| Create/update the default or any unscoped connection | Platform admin |
| Change the default connection | Platform admin |
| Flip the global Enable switch / bot mode | Platform admin (one-time platform step) |
| Select an app on a workflow (`slack_connection_id`) | Anyone who can edit the manifest; unknown IDs fail validation. Saving a workflow's own bot selects it automatically. |
| Select an app on a project (`slack_connection_id`) | Product owner (reads need product access); unknown IDs fail validation |
| Delete a connection | Blocked while it is the default or still selected by any workflow or project |
| Add a channel route on a scoped connection ("One of my bots") | Someone who manages the connection (owner of its workflow/crew, or admin) **and** can write the destination (workflow Owner/Editor, or owner of the crew project) |
| Remove a channel route | The same two checks; if the destination no longer exists, managing the connection is enough |
| List "bots I can use" (`GET …/slack/connections/mine`) | Returns only scoped connections the caller manages, with their routes and no tokens |

Bot-route principals can never manage connections. WhatsApp turns are not
bot-route principals: they run as the paired owner (`bot_owner` — the bot is
linked as the owner's number, so there is no channel grant to hold) and pass
these gates as the owner would. All API responses carry masked tokens only.

## Bot principals (`bot_route` vs `bot_owner`)

- `bot_route` is stamped on Slack turns only, and only when the request
  carries a channel grant (`applyBotRouteClaims` in
  `agent_go/cmd/server/bot_session_starter.go`). It is a synthetic route
  identity: the Slack sender gets no authority beyond the grant, and
  `revalidateExecutionPrincipal` re-checks the live route config on every
  tool call (connector enabled, route present, explicit grant, destination
  unchanged, sender email allowed). Execution is pinned to `run` access.
- `bot_owner` is stamped on WhatsApp turns. The sender was authenticated at
  message ingress (pairing ownership, link codes, per-slug workflow access),
  so the turn executes as the paired owner on the owner's data with nothing
  further to revalidate — the same standing as the owner's own app turns.
- The shared tool boundary (`bindToolExecutionContext` in
  `agent_go/cmd/server/tool_execution_context.go`) admits exactly these two
  principals on bot-marked sessions. Anything else bound there is rejected
  as `session origin changed`: before `bot_owner` existed, the guard
  admitted only `bot_route`, which silently broke every tool call in every
  WhatsApp turn.

## Key files

| Area | File |
|---|---|
| Registry model, normalize, migrate, encrypt | `agent_go/cmd/server/services/slack_connections.go` |
| Runtime mux, children, routing | `agent_go/cmd/server/services/slack_service.go` |
| Connection threading, scoped helpers | `agent_go/cmd/server/services/bot_connector.go` |
| Connections API + permission gates | `agent_go/cmd/server/slack_connection_routes.go` |
| "One of my bots" API (`/mine`, `/{id}/channel-routes/{channel}`) | `agent_go/cmd/server/slack_connection_channel_routes.go` |
| Manifest selection + validation | `agent_go/cmd/server/workflow_manifest*.go` |
| Project selection + product ownership | `agent_go/cmd/server/slack_connection_routes.go` (`productSlackConnectionID`, `requireProductSlackScopeOwner`) |
| Own-bot routing rule | `agent_go/cmd/server/services/slack_dedicated_route.go`, `agent_go/cmd/server/slack_dedicated_route.go` |
| Tool owner branches | `agent_go/cmd/server/slack_bot_tools.go` |
| Slack tab (own bot, one of my bots, shared bot) | `frontend/src/components/workflow/bots/SlackSetup.tsx`, `useWorkflowBots.ts` |
| Admin shared bot + bot list (Access → Slack) | `frontend/src/components/admin/SlackAdminPanel.tsx` |
| Slugs: routing, resolution, re-checks | `agent_go/cmd/server/slack_slugs.go`, `agent_go/cmd/server/services/slack_targets.go`, `agent_go/cmd/server/services/slack_slug_routing.go` |
| Slugs API (`/slack/targets/…`, `/{id}/targets`) | `agent_go/cmd/server/slack_slug_routes.go`, `agent_go/cmd/server/slack_connection_channel_routes.go` |
| Slack tab: AgentWorks bot switch, slug, channels | `frontend/src/components/workflow/bots/SlackSlugsSection.tsx` |
| Agent guidance | `agent_go/cmd/server/guidance/templates/system/slack-bot-routing.md` |
