[← integrations / slack](index.md)

# PLAT-668: Slack slugs: one bot for many Crews and workflows

| Field | Value |
|---|---|
| State | in progress |
| Priority | P2 |
| Product | integrations |
| Area | slack |
| Summary | One Slack app serves many workflows and Crews: slug routing in channels (thread-bound, allowed list, buttons) and DMs |

## Why

Owner, 2026-10-07: a free Slack workspace caps installed apps, and today a channel reaches only one target per app and
a DM always reaches the bot's own target. On RTS the owner wants the workflows' Slack bot to also serve 3 Crews,
possibly in a single channel.

## Decisions (owner, 2026-10-07)

- Pick with `@bot <slug> …` (first word after the mention).
- No slug in a channel without a default: the bot asks with one button per allowed target; the pick binds the thread.
- DMs reach only this app's slugs, and only targets the person can access.
- Applies to own bots and to the shared admin bot (whose DMs stay off).
- Targets are workflows and Crews alike, and Codes (DMs only, owner only).
- Two ways to connect with the same routing: the platform bot (one per server, set up by an admin; the default) and own
  bots (for another Slack workspace or a custom name). The platform bot accepts DMs.
- Opt-in per target: nothing is reachable from Slack until its owner turns on "Use the AgentWorks bot"; turning it off
  cuts access at once.
- Slugs are target names (editable); a clash is resolved with buttons.
- A channel is added by the target's owner, only if they are a member of it.

## Design

[docs/design/slack_slugs.md](../../../../design/slack_slugs.md).

## Done

- Phase 1 (data model, migration, routing): `config/slack-targets.json` holds each target's slug and its "Use the
  AgentWorks bot" switch, plus owner-added platform-bot channel targets; an own bot's `channel_routes` entries gain
  `targets` (more allowed targets; the old destination is the default) and the connection gains `targets` (DM
  attachments, Codes included). Routes saved before slugs read as a one-target list that is also the default; a
  target with the admin's older channel route counts as switched on until its owner decides. Every message, turn
  and tool call resolves the turn's target against the channel's allowed list on its arrival app
  (`slack_slugs.go`, `services/slack_targets.go`); a DM turn against the targets the app offers and the sender can
  reach.
- Phase 2 (parsing, binding, buttons, DMs, API): a channel mention's first word picks a target only if it is on the
  channel's allowed list (otherwise it is message text); the thread is bound to the pick
  (`config/slack-threads/<hash>.target.json`) and every later turn re-checks the binding against the list; a different
  slug in a bound thread is refused; no slug and no default posts one button per allowed target and the click binds
  the thread and runs the waiting message. DMs: `<slug> ...` picks among the targets the sender can reach with their
  own access, the pick is remembered for the DM, `list` lists them; the platform bot now takes DMs. API:
  `/api/human-feedback/slack/targets` (settings, channels with the Slack member check, admin products) and own-bot
  `/connections/{id}/targets` (DM attachments, Codes included); own-bot channels list several targets.
- Phase 3 (UI, dry run): each target's Slack tab has a "Use the AgentWorks bot" section (switch, editable slug,
  channel cards with every allowed target and the default, add a channel you are in, Test = dry run with the slug);
  a Code's tab offers the AgentWorks bot for DMs and "one of my bots" to answer its DMs through an own bot; own-bot
  channel chips list every target in the channel; Access → Slack limits which products may use the AgentWorks bot.
  The dry run takes a slug.

## Left

- Live check on RTS after a deploy (nothing is deployed): the workflows' bot plus 3 Crews in one channel, and a Code
  DM slug.
- The admin Slack page (Access → Slack) still edits the shared bot's admin routes one target per channel; owner-added
  targets show in each target's Slack tab.
- Phase 4: files on top-level channel mentions; Slack triggers on own-bot channel routes.
