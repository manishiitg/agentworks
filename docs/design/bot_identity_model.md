# Bots: who a turn runs as

Status: built 2026-09-26 — crew-in-Slack gaps and 1:1 Slack DMs as the sender (dry-run tested; not yet live-verified).
Decisions (user, 2026-09-26): identity by email match; the prompt names the sender with their email; a DM runs as the user only when it is a true 1:1 DM. Related: `bot_destination_scope.md`.

## Rule

A bot turn runs as a person only when the platform proves who that person is
and nobody else is reading. Otherwise it runs as the destination in Run mode.

| Where the message comes from | Runs as | Mode |
|---|---|---|
| WhatsApp (the paired user's own chats) | the paired user | from that user's access: owner/editor → full, reader → Run |
| Slack channel, private channel, group DM | the route (the workflow or crew) | always Run |
| Slack DM with the bot (new) | the AgentWorks user matched to the sender | from that user's access, same as WhatsApp |

Mode always comes from access, never from the platform or a saved setting:
`readOnlyForRequest(access)` → `resolveWorkflowChatPolicy` already turns write
access into Builder and read access into Run. The only new question per
platform is **whose access**.

## Today (checked in code, 2026-09-26)

**WhatsApp already follows the rule.** The pairing is the user's own number;
only their self-chat and chats they bound with a link code are accepted
(`isAllowedDM`). Turns carry `Provider: "bot_owner"` with the paired user's id
(`applyBotRouteClaims`), and `conversationTargetAccess` answers from that
user's access, so a workflow reader gets Run and an owner gets full.

- Other owners' crews (fixed 2026-09-26): crews are read-only for everyone
  but their owner, so WhatsApp's `@list` shows them in an "Other crews
  (read-only)" section (from the crew directory, never auto-routed) and
  `@switch` reaches them. A message runs as the paired user, in Run mode, in
  their own reader chat of that crew. The bot manager keeps the paired user
  as the session user for WhatsApp, like a DM: a crew route never swaps in
  its owner (which would have run someone else's crew as its owner).
- One user, one chat on WhatsApp too: a crew message continues the user's own
  crew chat (resolved as the web does, `senderProfileTurn`), and a workflow
  message continues the Builder chat the web restores for them, instead of
  WhatsApp's separate per-chat conversation.
- One person, one WhatsApp (user, 2026-09-26): an account links a single
  phone. Extra phones (`phone-2`, …, built for a second SparkQuill parent)
  were removed; startup logs any left over out of WhatsApp.

**Slack channels already run in Run mode.** `revalidateExecutionPrincipal`
forces `BotGrant = "run"` and `Access = read`. A crew route runs in its owner's
namespace (`ResourceOwnerID`), and a workflow route runs as a synthetic
`bot-…` principal. The sender is only an audit actor and an email allow/block
list.

**Slack DMs did not work at all** before this change: the app subscribed to
`app_mention`, `message.channels` and `message.groups` only, and
`handleSocketModeMessage` dropped every top-level message that wasn't a
thread reply.

## Slack DMs as the sender

### Identity

Slack gives the sender's user id; `users.info` gives the profile email
(`users:read.email`). Matching that email to a `users.json` record is what
maps a DM to an AgentWorks user. Email alone is only as strong as the Slack
workspace's email control, so a DM runs as a user only when all of these hold:

1. The sender is a full member of the app's own Slack team: not a guest
   (`is_restricted`, `is_ultra_restricted`), not an external Slack Connect
   user (`is_stranger`, or a different `team_id`), and not a bot.
2. The email matches exactly one `users.json` record that is not disabled.
3. The conversation is a 1:1 DM between the sender and the bot, checked with
   Slack itself (`conversations.info` → `is_im`, and its `user` is the
   sender), not inferred from the `D…` id or the event's `channel_type`. A
   group DM (`mpim`), a private channel or a channel is a group: Run mode.

If any check fails, the DM falls back to the channel rule (the route in Run
mode) when the bot has one, or the bot says who it can't identify.

### Destination

- **Its own bot** (a workflow's or crew's dedicated app): the DM is for that
  workflow or crew. The mapped user's access to it decides the mode; no
  access means a refusal, not a Run-mode fallback.
- **The shared bot**: a DM names no destination. Phase 1 replies that DMs work
  with a workflow's or crew's own bot. A later phase can reuse WhatsApp's
  `@slug` picker, which already lists only what the user can access.

### Principal

Built as: `services/slack_dm.go` (ingress proof: `directMessage`,
`handleSlackDirectMessage`), `slack_dm.go` (account lookup
`slackDMUserForEmail`, `revalidateSlackDMPrincipal`), the DM flag carried in
the thread's bot metadata (`_trusted_slack_dm` on each request), and
`applyBotRouteClaims`. A DM turn uses a new provider, `bot_user`: `UserID` = the mapped user,
`Provider` = `bot_user`, and the Slack user id as the audit actor. It is
revalidated at the query boundary and on every tool call, like `bot_route`:

- the connection still exists and is still the destination's own app;
- the Slack user still maps to the same AgentWorks user (email changed,
  account disabled or removed → refuse);
- the DM channel still belongs to that Slack user.

Access comes from `conversationTargetAccess` with the mapped user's claims.
It is not stored on the session, so a demotion applies on the next turn and
the next tool call.

### Chats

One user, one chat (user, 2026-09-26). A DM continues the sender's own chat
of the crew: the same conversation their web chat opens (and WhatsApp
continues), found exactly as the web finds it (`resolveConversationBindingForUser`
plus the sender's own conversation registry, `slackDMProfileTurn`). Every DM
thread continues that one chat; an owner's is the crew's own chat, a reader's
is their reader chat. Slack channel threads are different: each is its own
chat of the crew (a group conversation, run as the route).

Workflows the same way: a DM continues the sender's own chat of the workflow,
the session their web Builder restores (`handleGetWorkflowBuilderSession`:
their live Builder session, else their latest saved Builder conversation,
which is private to them). With none yet, the DM starts one, and it becomes
the chat the web restores. The turn runs as the sender, not the route's bot
principal; the model and its credentials still come from the workflow's own
LLM settings, as for every Builder turn.

Switching surfaces inside one chat: a bot turn marks the live session as a
bot session (platform, `bot:` trigger, Slack binding). When the session's own
owner then sends a plain web turn, `clearBotOriginForOwnerTurn` removes those
marks, so the web turn runs as an interactive turn with working tools. Anyone
else, and scheduled, child or bot turns, never clear them.

Attachments are checked as the sender, as in their web chat: a crew with an
owner-only attachment refuses a reader's DM until the attachment is removed.

### Setup

The app needs the `message.im` event and the `im:history` scope; Save & test
reports them as missing, like the other scopes. The dry run gains a DM case.

## Crew turns in Slack: what they get

This is separate from identity. These are the gaps the 2026-09-26 crew report
found. They apply to channel turns (Run mode) and DM turns alike:

1. **Thread history on the first turn** (shipped). A crew's Slack thread
   chat now gets the thread (`buildQueryWithThreadHistory`) on its first
   turn; follow-ups already got catch-up.
2. **The Slack read tool** (shipped). Run-mode crew turns register the
   `slack` CLI (held to the arrival channel and destination); setup changes
   still need write access.
3. **MCP servers (Notion etc.)** (shipped, diagnosis from code, not
   production logs). The servers were loaded; Run mode lacked
   `list_mcp_servers` (only `mcp_management` registered it), and the crew's
   `work-mcp` skill forbids claiming a server before checking it. New
   capability `mcp_inspection` (Run and Builder) registers only
   `list_mcp_servers`. Confirm on RTS with `[CHAT_POLICY] MCP management
   admission: ... inspect=true` on a Slack turn.
4. **Who is asking** (shipped). Every Slack turn starts with
   `From: <name> <email> (Slack)` (`withBotSender`), and a thread's session
   is titled `<name>: <first message> · #channel` so its tab is not just
   "Slack".

## Delivery (added 2026-09-26)

- A DM is one continuous chat: the bot replies directly in the DM, not in
  threads; top-level messages share one conversation keyed by the DM channel.
- A follow-up sent while a turn runs steers the running CLI in every bot
  conversation, as in the web chat (`shouldTryRetainedDeliveryBeforeQueue`).
- The `slack` tool opens every Slack Web API method for trusted full-mode turns
  and keeps the read-and-reply allowlist for Run mode (user decision).

## Costs

Checked 2026-09-26 against the cost ledger (`costobserver`, `cost_overview.go`):

- **Who pays does not change.** Workflow turns use the workflow's own LLM
  settings and crew turns the crew's engine, whoever asks; the platform
  credentials behind them are the same.
- **Attribution per LLM call:** `user_id` is who the turn runs as — the
  sender for a DM, the paired user for WhatsApp, the crew owner for a crew
  channel turn, the route's `bot-…` principal for a workflow channel turn.
  `workflow_id` is the workflow or crew folder (a reader's crew DM counts on
  that crew's row, in the same folder form as web turns), and
  `source_platform` comes from each turn's own request, so a chat mixing web
  and DM turns still splits by platform.
- **Views:** Costs → overview groups by workflow/crew row, not by user, and
  only admins see chat/other spend; nothing reads `user_id`, so no view moves.
  Workflow DM turns also land in the workflow's own `costs/costs.sqlite`
  (scope builder), like web Builder turns; crews have no per-crew ledger.
- **Grouping by session changes:** one chat per user means a DM and its web
  turns share a session, so "per session" totals now cover both surfaces
  (before, each Slack thread was its own session).

## Tests

Dry-run cases (`bot_dry_run_test.go`), from what the UI saves:

- channel mention by the crew owner → Run mode, owner namespace;
- DM by the crew owner to the crew's own bot → full mode, owner's chat;
- DM by a reader of a shared crew → Run mode, reader's chat;
- DM by a user with no access → refused;
- DM by a guest, an external user, or an unmatched email → not run as a user;
- group DM → Run mode;
- DM to the shared bot → pointer reply;
- a user demoted between turns → the next turn is Run mode.
