[← integrations / slack](index.md)

# PLAT-668: Slack slugs: one bot for many Crews and workflows

| Field | Value |
|---|---|
| State | open |
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
- Targets are workflows and Crews alike.

## Design

[docs/design/slack_slugs.md](../../../../design/slack_slugs.md).

## Left

Everything: build the slug table, multi-target channel routes and their migration, channel and DM parsing, thread
binding, the button prompt, the UI, the dry run, and the two side fixes (files on top-level mentions, triggers on
own-bot routes). Verify live on RTS with the workflows' bot and 3 Crews in one channel.
