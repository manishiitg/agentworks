# Slack slugs: one Slack app for many workflows and Crews

Ticket: [PLAT-668](../bugs/pulse_platform/integrations/slack/plat-668.md). Owner decisions 2026-10-07.

## Why

A free Slack workspace caps the number of installed apps (about 10). Today each Slack app either answers for one
target (its "own bot") or is reused per channel ("one of my bots"), and **a channel reaches exactly one target**. A team
that wants one bot serving several workflows and Crews, possibly in one channel (RTS: the workflows' bot plus 3
Crews), cannot do it, and in a DM the bot always answers for its own target. WhatsApp already solves this with
`@slug` routing; Slack should work the same way.

## Today (for reference)

- Socket Mode apps, tokens pasted from a generated manifest (`config/slack-config.json`, encrypted).
- "Who answers?": its own bot (scoped to one workflow or Crew), one of my bots (an own bot plus `channel_routes`
  pointing a channel to this target), or the admin shared bot (`bot-connectors.json` → `slack.allowed_channels`).
- Channel turns run as the route in Run mode; a DM to an own bot runs as the AgentWorks user matched by email, with
  that user's own access. The shared bot refuses DMs.
- WhatsApp: a per-pairing slug → `ChannelRoute` map (auto-filled, editable), an active slug per chat, and "a saved slug
  never confers access" (the person's access is checked).

## Design

Targets are **workflows and Crews alike** (and a Code project, DMs only, as today). Everything below applies to own
bots and to the shared admin bot.

### 1. A slug table per Slack app

- Each Slack app connection gets `slugs: { <slug>: ChannelRoute }`.
- A target that uses the app gets a slug: its own target, and every target that picks it through "one of my bots".
  The default slug is the target's name, lowercased (`[a-z0-9-]`, the WhatsApp rule); the owner can rename it. Slugs
  are unique per app.
- The shared bot's slugs are managed by an admin, alongside its channel routes.

### 2. Channels reach several targets

- A channel route becomes **channel → allowed slugs + an optional default slug** (instead of one target).
- Migration: every existing channel route becomes a list of one slug that is also the default, so current setups
  behave exactly as before.
- Someone in the channel can only reach the slugs on that channel's list.

### 3. Picking a target in a channel

- **Syntax:** `@bot <slug> …`. If the first word after the bot mention matches one of the channel's allowed slugs,
  it picks that target and is removed from the message; otherwise the whole text is the message.
- **The thread is bound** to the picked target (stored with the thread's durable session binding). Follow-ups in the
  thread go to the same target without a slug. A different slug in a bound thread is refused with a pointer to start a
  new thread.
- **No slug:** the channel's default target answers; with no default, the bot replies in the thread with **one button
  per allowed target**, and picking one binds the thread (Socket Mode interactivity, already enabled).
- Runs as today: as the route, Run mode, `blocked_emails` honoured, the run-time revalidation of the route on every
  tool call (now including "this slug is still allowed in this channel").

### 4. Picking a target in a DM

- `@bot <slug> …` or a message starting with `<slug>` picks the target; the choice is **remembered for that DM** until
  another slug is used. `list` replies with the slugs available to this person.
- **Scope:** only this app's slugs, and of those only targets the matched AgentWorks user can access (checked as the
  person on every message, like WhatsApp). The shared bot keeps refusing DMs.
- With nothing picked yet, the app's own target answers (today's behaviour); a bot with no own target lists the slugs.

### 5. Setup UI

- The bot's Slack tab gets a **Slugs** list (target, slug, rename, remove).
- Each channel card shows its **allowed slugs** and **default**; adding a target to a channel is a multi-select.
- "One of my bots" in a workflow's or Crew's Slack tab adds that target's slug to the chosen app and lets the owner pick
  the channels it may answer in.
- The dry run accepts a slug and a channel, so "Save & test" proves the route.

### 6. Fixed along the way

- Files attached to a top-level channel @mention are downloaded like thread replies and DMs (the `app_mention` path
  never called `appendSlackFileContext`).
- Slack triggers also work on own-bot channel routes, not only the shared bot's.

## Security

- A slug is a name, never a grant: channel access comes from the channel's allowed list (set by the bot's owner or an
  admin); DM access is the person's own access to the target.
- Slug parsing happens before routing; a slug not on the channel's list is treated as message text, never as a target.
- Thread bindings record the slug and are revalidated on every turn; removing a slug from a channel stops its threads.

## Out of scope

- OAuth "Add to Slack" install, Events API (Socket Mode stays).
- DMs to the shared bot.
- Several targets answering in one thread.
