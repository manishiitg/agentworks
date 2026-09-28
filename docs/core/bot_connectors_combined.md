# Bot Connectors: Architecture, Configuration, and Review

Reviewed against `main` at `ca3cf76cb` on 2026-09-20.

This document consolidates [Bot Connector System](bot_connector_system.md),
[Channel connectors](../channel-connectors.md), and the implementation review.
It describes the checked-out implementation. Slack setup requirements below
reflect the application's diagnostics; no live Slack or WhatsApp installation
was tested during this review.

## Current scope and recent work

Slack and WhatsApp are the registered messaging bot transports. Discord and
Telegram are extension targets exercised through mock capability tests, not
implemented live connectors. The web simulator has been removed: its remaining
service code is not registered and its former HTTP routes are unavailable.

Recent work includes:

- Workflow-specific Slack channel routes and WhatsApp slug routes.
- Route authorization, Run-mode workflow access, and clearer access failures.
- Shared channel capabilities for streaming, edits, reactions, and progress.
- Durable conversation bindings and continuity across turns and restarts.
- Separate editable follow-up replies and reaction cleanup.
- Slack diagnostics for scopes and Socket Mode, plus manual delivery checks.
- Slack triggers with shared JSON matching/mapping and bounded channel context.
- Connector settings in the workflow capabilities panel.

## Architecture

```text
Slack / WhatsApp
    -> adapter: decode events, resolve route, normalize message
    -> BotConversationManager: authorize, start/resume, handle follow-ups
    -> prepare workflow/profile route or saved chat capabilities
    -> shared query/session execution
    -> BotEventFilter: replies, progress, blocking input, completion
    -> platform formatter and outgoing messages
```

| Component | Source relative to repository root | Responsibility |
| --- | --- | --- |
| Connector and conversation manager | `agent_go/cmd/server/services/bot_connector.go` | Contract, route integration, lifecycle |
| Event filter | `agent_go/cmd/server/services/bot_event_filter.go` | Replies, progress, feedback, completion |
| Event adapter | `agent_go/cmd/server/bot_event_adapter.go` | Bridge session events into the service interface |
| Session starter | `agent_go/cmd/server/bot_session_starter.go` | Enter shared session execution |
| Slack adapter | `agent_go/cmd/server/services/slack_service.go` | Slack transport and messages |
| WhatsApp adapter/manager | `agent_go/cmd/server/services/whatsapp_service.go`, `agent_go/cmd/server/services/whatsapp_manager.go` | Transport and account/device management |
| Configuration API | `agent_go/cmd/server/bot_routes.go`, `agent_go/cmd/server/bot_config_routes.go` | Connector and shared settings |
| Workflow UI | `frontend/src/components/workflow/WorkflowBotsPanel.tsx` | Workflow routes and setup |
| Slack diagnostics | `agent_go/cmd/server/services/slack_connection_diagnostics.go` | Shared settings/Builder connection test |

Server startup wires execution callbacks, workflow access, route preparation,
event subscriptions, and secrets loading into the manager.

## Connector contract

```go
type BotConnector interface {
    NotificationConnector

    Capabilities() ChannelCapabilities
    StartListening(ctx context.Context) error
    StopListening()
    SendThreadMessage(ctx context.Context, threadID ThreadID, message string) (string, error)
    SendThreadMessageWithBlocks(ctx context.Context, threadID ThreadID, message string, blocks []MessageBlock) (string, error)
    UpdateMessage(ctx context.Context, threadID ThreadID, messageID string, newText string) error
    AddReaction(ctx context.Context, channelID, messageTS, emoji string) error
    RemoveReaction(ctx context.Context, channelID, messageTS, emoji string) error
    GetThreadHistory(ctx context.Context, threadID ThreadID) ([]ThreadMessage, error)
    GetChannelName(ctx context.Context, channelID string) string
    SetMessageHandler(handler BotMessageHandler)
    SetInteractionHandler(handler BotInteractionHandler)
    GetFormatter() MessageFormatter
}
```

`NotificationConnector` supplies `Name`, `IsEnabled`, and `SendNotification`.
`SupportsThreads()` is no longer the contract. Zero capability values disable
features.

| Capability | Behavior | Slack | WhatsApp |
| --- | --- | --- | --- |
| `Threads` | Read and continue threads | Yes | No |
| `MessageEdits` | Update replies/progress | Yes | No |
| `StreamingReplies` | Stream text; also requires edits | Yes | No |
| `Reactions` | Acceptance/processing indicators | Yes | No |
| `MessageDeletion` | Remove temporary messages; requires `BotMessageDeleter` | Yes | No |
| `ProgressUpdates` | Temporary status; requires edits or deletion | Yes | No |
| `WorkflowProgress` | Permit route-requested workflow detail | Yes | Yes |
| `ChannelHistory` | Optional bounded `ChannelHistoryReader` | Yes | No |

Adapters translate common reaction names to their platform representation.
History callers authorize the source channel; adapters enforce bounded time,
message, and byte budgets. Attachment JSON remains opaque to the shared history
interface. Capabilities describe presentation, not authorization.

## Configuration and authorization

### Routes and identity

Workflow connector settings live in the workflow capabilities panel. Slack uses
channel routes; WhatsApp supports slug routes. Preparation can select a workflow
or product/profile conversation instead of generic multi-agent chat.

- Slack workflow routes execute through a route-scoped bot principal and a
  workflow access check. Sender information remains audit metadata. Slack also
  applies route-specific email restrictions when configured.
- WhatsApp workflow routes use the paired workspace account and its workflow
  access. A saved slug identifies a destination; it does not itself grant access.
- Product/profile routes carry their own owner and conversation metadata.
- Generic chat must resolve a workspace identity. `_global.allowed_emails`,
  merged with `BOT_ALLOWED_EMAILS`, filters generic messages with a resolved
  email. It is not a universal gate for configured routes.

Current deployed workflow routing is oriented around Run access. Preserve
authorization and execution scope on follow-ups and resumes as well as starts.

### Session configuration sources

These are the generic request builder's sources. Routed Slack workflow requests
can return earlier through the dedicated workflow-turn preparer; generic
defaults must not be assumed to override routed workflow configuration.

| Setting | Current source |
| --- | --- |
| Servers, skills, tools, code-execution mode | User's `_users/<id>/multiagent-config.json` |
| Selected global secrets, browser settings, notification reference | Saved chat capabilities when present |
| Saved primary LLM configuration | Saved chat capabilities when present |
| Delegation tiers | Workspace `config/delegation-tier-config.json` |
| Provider API keys | Encrypted workspace provider-key storage via `LoadProviderKeys` |
| User secrets | Server-side loader for the resolved workspace user |
| Workflow and conversation metadata | Route preparation and active conversation |

Without saved chat capabilities, the generic builder selects no MCP servers and
discovers skills. An explicit saved configuration is handled as saved.
`_global.default_servers`, `_global.default_skills`, and `_global.provider_api_keys`
are not the runtime sources for this builder.

### Configuration API and storage

| Method | Path | Purpose |
| --- | --- | --- |
| GET | `/api/bot/connectors` | List connector settings/status |
| GET | `/api/bot/connectors/{platform}` | Read settings |
| POST | `/api/bot/connectors/{platform}` | Save settings |
| POST | `/api/bot/connectors/{platform}/test` | Test configuration |
| GET / POST | `/api/bot/config` | Read/save shared `_global` settings |

The shared API still accepts legacy tier, provider-key, server, and skill fields.
Stored fields are not necessarily consumed by the current request builder.
The old `PUT /api/bot/connectors/_global` is not the registered save method.

Bot sessions are regular chat sessions with bot metadata on their on-disk
manifest. The old SQL table description does not describe current persistence
adequately.

## Conversation lifecycle

### Slack

1. An explicit mention opens a conversational bot thread after access checks.
   Independently configured Slack triggers use a separate entry path.
2. The manager prepares execution and starts the event filter.
3. While running, mentions can deliver follow-ups. Plain replies can also be
   accepted in single-user threads. The running-session path requires a mention
   when history contains multiple users or cannot be read.
4. Blocking sessions process answers through their blocking-response path.
5. Completion clears reactions and retains the session entry. It does not post
   the old generic “Session completed.” status message.
6. Later turns can reuse the conversation/session identity with a separate
   execution. The completed-session path also accepts admitted plain replies;
   the running-session mention rule must not be assumed to cover every state.

Non-mention messages require an existing session or a valid durable binding
before entering the manager. A channel route alone does not make every ordinary
message start a conversational session.

A thread reply that tags another user but not the bot is addressed to a
colleague: the bot stays silent (no reply, no run, no reaction) no matter how
few people are in the thread, and such a reply never revives a conversation
after restart. Blocking answers and control commands still bypass this rule,
and a message that tags the bot (even alongside others) is processed. Other
users' tags on processed messages are rewritten to @DisplayName so model
turns read names instead of opaque IDs.

### Persistence and thread-less conversations

Slack saves per-thread bindings under `config/slack-threads/<hash>.json`.
Restoration checks the route key, preventing a changed route from adopting old
history. WhatsApp also supports durable bindings, isolated by account/device.

An hourly janitor prunes completed/failed in-memory entries after seven days of
inactivity. This is not immediate deletion at completion or deletion of all
persisted history.

Thread-less conversations normally use a one-hour inactivity window. A later
message can start a fresh conversation with a short prior-history preamble;
product/profile conversations have separate continuity behavior. Changing a
thread-less route creates a conversation boundary to avoid mixing workflows.

Thread-less controls require `@`: for example `@status`, `@resume`, `@continue`,
`@full`, and `@concise`. Bare `@resume` opens a picker; a selector can
identify a session directly. There are no session end commands: words like
`done`, `reset`, or `stop`, with or without `@`, are delivered to the agent
as ordinary text. Conversations end through the inactivity window, route
changes (`@switch`/`@off`), or plan rejection.

## Events, feedback, and output

The filter handles streaming chunks, main-agent text, delegation lifecycle,
completion, plan approval, human feedback, and errors. Capabilities and detail
mode control presentation. Follow-ups have separate editable reply boundaries.

Completion normally requires a completion signal, no pending delegations, and
no blocking input. Mirrored workflow work can defer manager cleanup until it
drains. Temporary progress is cleaned up on completion, cancellation, and
blocking input. If deletion fails, editable progress can become terminal status.

| Blocking event | Handling |
| --- | --- |
| Plan approval | Approval sends “Approved. Execute the plan.”; rejection cancels; other text becomes feedback |
| Human feedback with request ID | Submit through `NotificationManager.ReceiveNotification` to the waiting operation |
| Human feedback without request ID / fallback | Deliver a session follow-up |

Human feedback is not always a new agent turn. Preserving the request ID matters
for resuming the waiting operation.

With `PUBLIC_URL`, supported workspace paths in Markdown links and bare paths
are converted to file links. URLs use the filter's user identity when available;
they are not always generated with `uid=default`.

## Slack setup and diagnostics

The shared diagnostic checks the bot token, granted scopes, and the app token's
ability to open a Socket Mode connection. It posts no test message and returns
neither credentials nor WebSocket URLs.

Required bot scopes checked by the implementation:

```text
app_mentions:read  chat:write       reactions:write
channels:history   groups:history  channels:read
groups:read        users:read      users:read.email
```

`files:read` and `chat:write.public` are optional feature scopes. Missing granted
scopes require updating permissions and reinstalling the app. If Slack does not
return granted scopes, diagnostics require manual verification.

Manual delivery checks remain required even when token checks pass:

1. Enable Socket Mode in the same Slack app.
2. Enable and save `app_mention`, `message.channels`, and `message.groups` event
   subscriptions. Socket Mode does not require a Request URL.
3. Invite the bot to the target channel.
4. Verify an initial mention and a plain reply in its single-user thread.

Tokens cannot read event-subscription settings. Passing the connection test is
not proof of end-to-end delivery.

## Who a Slack turn runs as

- **WhatsApp** runs as the paired user, in their own mode, and continues
  their own chat: their crew chat, or the Builder chat the web restores for a
  workflow. `@list` also shows other owners' crews under "Other crews
  (read-only)"; those run in Run mode in the user's own reader chat.
- **Channels, private channels and group DMs** are groups: the turn runs as
  the route (the workflow or crew), always in Run mode. The sender is named in
  the prompt (`From: <name> <email> (Slack)`) and kept as the audit actor.
- **A 1:1 DM with a workflow's or crew's own bot** runs as the sender's
  AgentWorks account, in that account's own mode: owner or editor gets the
  full chat, a reader gets Run mode, anyone without access is refused. The
  sender must be a full member of the app's Slack team (no guests, no Slack
  Connect users), Slack must confirm the conversation is a 1:1 IM with them,
  and their Slack email must match exactly one enabled account in
  `users.json`. The query boundary and every tool call re-check the mapping.
  One user, one chat: a DM continues the sender's own chat — for a crew, the
  chat their web UI (and WhatsApp) continues; for a workflow, the Builder chat
  their web UI restores — whichever DM thread it arrives in. The shared bot
  does not take DMs.
- **Replies and follow-ups.** In a 1:1 DM the bot replies directly (not in a
  thread): top-level DM messages share one conversation keyed by the DM
  channel (`slackThreadOption` skips `thread_ts` when the thread is the
  channel). A reply made inside a DM thread is answered in that thread, in the
  same chat. Channel threads are unchanged. In every bot conversation (DM,
  channel thread, WhatsApp) a message sent while a turn runs steers the running
  CLI, like the web chat; schedules, webhooks and Slack trigger runs queue.
- **The agent's `slack` tool** has two modes. In an owner's full-mode chat
  (web, WhatsApp, 1:1 DM) it takes any Slack Web API method (e.g.
  `views.publish` to set the bot's App Home tab; the Slack tab has an "Ask AI
  to publish the Home tab" button). In Run mode (channels, read-only users) it
  keeps the channel-scoped read-and-reply allowlist, since anyone who can post
  in a channel can steer the agent. The token stays backend-owned either way.

DMs need the `im:history` and `im:read` scopes, the `message.im` event, and
App Home → Messages Tab with "Allow users to send Slash commands and messages
from the messages tab". The generated app manifest sets all of these;
"Save & test" reports missing DM scopes without failing channel bots.

Sources: `agent_go/cmd/server/services/slack_dm.go`,
`agent_go/cmd/server/slack_dm.go`, `docs/design/bot_identity_model.md`.

## Slack workflow triggers

Route owners can configure `human_message` or `trusted_app` triggers with
matching, payload mappings, route selections, and optional bounded context.
Trusted-app matching checks configured app/bot identity. The matcher excludes
ordinary thread replies, the bot's own user messages, and unsupported subtypes.

Context is restricted to the triggering channel and excludes later messages.
Validated limits are 1–100 messages and 1–1440 lookback minutes, with optional
thread inclusion. Trigger settings come from route configuration, not incoming
message instructions.

Sources: `agent_go/cmd/server/services/slack_trigger.go` and
`agent_go/cmd/server/slack_trigger.go`.

## Adding a connector

1. Implement `BotConnector`, its formatter, and explicit capabilities.
2. Normalize events into `BotIncomingMessage` and `ThreadID`.
3. Implement optional deletion/history interfaces for advertised features.
4. Integrate authenticated settings and routing, preserving target-scoped Run
   access and account/route isolation.
5. Register with the existing manager during startup.
6. Wire Builder tools/guidance through existing `product.yaml` policies.
7. Test transport behavior, authorization, feedback, completion, and restoration.

Mock tests using Discord or Telegram names establish shared behavior only, not
a working production integration.

## Documentation review and verification

Confirmed gaps in the original core document:

| Finding | Consequence | Correction here |
| --- | --- | --- |
| Universal `allowed_emails` claim | Wrong operational access boundary | Separate generic, Slack-route, and WhatsApp-account checks |
| `_global` claimed as runtime config source | Settings may not affect execution | Identify user/workspace sources and routed preparation |
| Mention-only and immediate removal claims | Wrong continuation expectations | Describe running/completed states and durable bindings |
| Obsolete interface | New adapter cannot satisfy contract | Include capabilities, reactions, and channel-name methods |
| Removed simulator/UI presented as current | Unavailable routes and setup instructions | State removal and workflow panel replacement |
| All feedback described as follow-up | Misses waiting-request delivery | Explain notification submission by request ID |

Seven targeted tests passed in `agent_go/cmd/server/services` during review:

- `TestChannelCapabilitiesDriveStreamingForAnyPlatform`
- `TestProgressCleanupUsesCapabilitiesRatherThanPlatform`
- `TestProductionChannelCapabilities`
- `TestBlockingHumanFeedbackResponseSubmitsNotification`
- `TestThreadlessCompletedSameRouteReusesSessionID`
- `TestThreadlessSessionRestoresDurableBindingAfterManagerRestart`
- `TestSlackWorkflowAuthorizationStillUsesRoutePrincipal`

These validate selected shared behaviors, not a full connector audit or live
Slack/WhatsApp delivery. This consolidation changes documentation only.

## Code review: findings requiring fixes

Reviewed the connector implementation on `main` at `ca3cf76cb`, focusing on
authorization, conversation lifecycle, and Slack event handling. The shared
architecture is reasonable, but the following reproduced bugs prevent sign-off.
All three findings are closed: the P1 and the mention-guard P2 are fixed
(see notes below), and the reset P2 is moot after the removal of session
end commands.
Source line references below refer to the reviewed revision.

### P1: WhatsApp workflow switches reuse the previous conversation

Source: [bot_connector.go](../../agent_go/cmd/server/services/bot_connector.go),
lines 902–907; route-change detection is at lines 1283–1292.

`authorizeWorkflowRouteForMessage` overwrites `active.RouteKey` and workflow
metadata before `handleExistingSession` snapshots `oldRouteKey`. The subsequent
comparison therefore sees the incoming route as the existing route and misses
the switch.

Reproduction: create a completed WhatsApp conversation for workflow A, install
a successful workflow-access callback, and send a message routed to workflow B
through `HandleIncomingMessage`. The new execution receives A's session ID.
This retains conversation history and risks carrying native resume state across
unrelated workflows instead of establishing a new conversation boundary.

Fix: compare the previous and incoming routes before mutating active session
state. Start a fresh conversation when the target changes. Add a regression
through `HandleIncomingMessage` with authorization enabled: the existing
route-switch test calls `handleExistingSession` directly and misses this ordering.

Fixed: `authorizeWorkflowRouteForMessage` now leaves the active session
untouched when the granted route differs from the session's stored route key
on a thread-less platform (`routeChangeKeepsSession`), so
`handleExistingSession` sees the switch and starts a fresh conversation. The
access check, `msg.PresetWorkflow`, and `msg.WorkspaceUserID` updates are
unchanged, so the fresh session still starts under the incoming route.
Regression coverage: `TestThreadlessRouteSwitchViaIncomingMessageStartsFresh`
(switch starts fresh, no restored session, `preset_query_id` targets the new
workflow) and `TestThreadlessSameRouteViaIncomingMessageReusesSession`
(same-route replies still continue the conversation).

### P2: Resetting a running session recreates its cleared binding — MOOT

Session end commands (`@reset`, `@done`, and aliases) were removed: the
`@reset` path no longer exists, so this reproduction cannot run. Residual
note: `runSession` cleanup still persists the binding unconditionally after
cancellation, which also fires for route-change cancels. If a canceled
session's persist ever lands after a newer session's binding, it could
overwrite it; no reproduction exists today.

### P2: Completed Slack threads bypass the multi-user mention guard

Source: [bot_connector.go](../../agent_go/cmd/server/services/bot_connector.go),
lines 1394–1408; the running-session guard is at lines 1366–1382.

The running-session branch ignores non-mention replies when a thread contains
multiple users. The completed/failed branch does not apply that guard and
immediately starts another execution with the existing conversation identity.

Reproduction: retain a completed Slack session whose thread history contains
Alice and Bob, then deliver Bob's ordinary reply to Alice with `IsMention=false`.
The start-session callback fires even though the bot was not addressed.

Fix: apply the multi-user mention policy before restarting completed or failed
threaded sessions, while retaining the intended blocking-feedback behavior.
Cover both running and completed states in regression tests.

Fixed: the running branch's ignore-with-reaction policy is now a shared
`ignoreMultiUserNonMention` helper, and the completed/failed branch applies
it before restarting. An outstanding blocking prompt still gets its answer:
like the running branch, awaiting messages route to `handleBlockingResponse`
first and bypass the guard. Thread-less platforms are unaffected (the helper
never ignores where threads don't exist). Regression coverage:
`TestCompletedMultiUserThreadNonMentionStaysOut` (the reported bug),
`TestCompletedMultiUserThreadMentionRestarts`,
`TestCompletedSingleUserThreadNonMentionRestarts`, and
`TestCompletedAwaitingFeedbackBypassesMentionGuard`.

### Code-review validation

- The full existing services suite passed: from `agent_go`, run
  `go test ./cmd/server/services -count=1`.
- Three temporary regression tests asserted the intended behavior and failed,
  confirming each finding: `TestReviewRouteSwitchDoesNotReuseOldConversation`,
  `TestReviewResetDoesNotResurrectBinding`, and
  `TestReviewCompletedMultiUserThreadRequiresMention`.
- The temporary tests were removed after reproduction; they are not committed
  regression coverage. Production code was not changed.
- Live Slack/WhatsApp delivery and the full server test suite were not exercised.

Existing passing tests therefore do not establish correctness for these three
lifecycle paths. Reproduce and fix them, then retain regression coverage before
closing the findings.
