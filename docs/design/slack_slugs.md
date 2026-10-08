# Slack slugs: one Slack app for many workflows, Crews and Codes

Ticket: PLAT-668. Owner decisions 2026-10-07.

Status: built on main (2026-10-07), not deployed; see the ticket for what is left. How it works in the code:
[docs/core/slack_connections.md](../core/slack_connections.md#slugs-one-bot-many-targets-plat-668).

## Why

A free Slack workspace caps the number of installed apps (about 10). Today each Slack app answers for one target (its
"own bot") or is reused per channel ("one of my bots"), a channel reaches exactly one target, a DM always reaches the
bot's own target, and a Code can only use its own bot. So every workflow, Crew or Code that wants Slack tends to cost an
app. On RTS the owner wants one bot serving the workflows plus 3 Crews, possibly in one channel, and Codes in DMs.

## Two ways to connect, one routing model

| | Platform bot (default) | Own bot |
|---|---|---|
| Slack app | one per server, set up once by an admin (today's shared bot) | created by a user, tokens pasted (today's own bot) |
| Slack workspace | the admin's | any workspace the user controls (a client's, another team's) |
| Reaches | targets whose owners turned on "Use the AgentWorks bot" | the targets its owner attached to it |

Both use the same slug routing below. An own bot is for a different Slack workspace or a custom bot name and icon.

## Opt-in per target

Nothing is reachable from Slack by default. A workflow, Crew or Code is reachable from the platform bot only after its
owner turns on **Slack → Use the AgentWorks bot** in its Integrations tab (one switch, no Slack app, no tokens). Turning
it off cuts all Slack access at once, including threads already bound to it (every turn and tool call is re-checked).
An admin controls whether the platform bot is enabled and may limit which products can use it.

| Target | DM | Channel |
|---|---|---|
| Workflow | people with access to it, each with their own access (owner full, reader Run) | channels its owner added, Run mode as the route |
| Crew | the owner and people it is shared with, each with their own access | channels its owner added, Run mode as the route |
| Code | the owner only, main chat (PLAT-571) | never |

## Slugs

- A slug is the target's name, lowercased to `[a-z0-9-]` (the WhatsApp rule); the owner can edit it. It is a name,
  never a grant: what it resolves to depends on who asks (DM) or where (channel).
- If two targets a person can reach share a slug, the bot asks which one with buttons.
- `list` replies with the slugs available here (this person in a DM, this channel's list in a channel).

## Channels

- A target's owner adds a channel in its Slack tab, but only a channel they are a member of (checked through Slack,
  `conversations.members` against the owner's email). Admins can still manage the platform bot's routes.
- A channel route becomes **channel → allowed targets + an optional default**. Existing routes migrate to a one-target
  list that is also the default, so current setups behave the same.
- **Pick with `@bot <slug> …`**: the first word after the mention, if it is one of the channel's allowed slugs, picks
  the target and is removed from the message; otherwise the whole text is the message.
- **The thread is bound** to the picked target (stored with the thread's durable session binding and revalidated every
  turn). Follow-ups need no slug. A different slug in a bound thread is refused with a pointer to start a new thread.
- **No slug:** the channel's default answers; with no default, the bot replies in the thread with one button per allowed
  target, and the pick binds the thread.
- Runs as today: as the route, Run mode, `blocked_emails` honoured, and the route revalidated on every tool call (now
  also "this target is still allowed in this channel and still opted in").

## DMs

- The sender is matched by Slack email to exactly one AgentWorks account, as today (guests, external Slack Connect users
  and other teams refused; group DMs refused).
- `@bot <slug> …` or a message starting with a slug picks the target; the choice is remembered for that DM until
  another slug is used. With nothing picked, the bot lists what the person can reach (an own bot with its own target
  answers for that target, as today).
- The platform bot now accepts DMs (today it refuses them). Access is the person's own access, checked every message.

## Own bots

The same model, scoped to the bot: its slugs are the targets its owner attached (its own target plus any picked with
"one of my bots", now including Codes for DMs). The same channel lists, binding and DM rules apply.

## Setup UI

- Each target's Slack tab: a **Use the AgentWorks bot** switch (with its slug, editable), its channels, or an own bot.
- Channel cards show the allowed targets and the default.
- Admin: the platform bot's tokens, enable switch and which products may use it.
- The dry run takes a slug and a channel or DM, so "Save & test" proves the route.

## Fixed along the way

- Files attached to a top-level channel @mention are downloaded like thread replies and DMs.
- Slack triggers also work on own-bot channel routes.

## Out of scope

- An OAuth "Add to Slack" install, the Events API (Socket Mode stays).
- Several targets answering in one thread.
