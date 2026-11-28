# Bots

A bot is one agent of a project on the server that its maker shares so other people (and their agents) can use it
without joining the project. Each person who uses it gets their own private chat with it. A bot is not People
(co-owners and Users work *in* the project: `crew_owners`) and not the dashboard (shared by its link).

One bot is one agent chat. A project with five agents has five bots only if all five are switched on; there is no
project-level bot. Projects on a Mac do not make bots, but can add bots from the servers the Mac is signed in to.

Use the `bots` tool (in a project chat, `get` and `set` default to this project).

## Make or change a bot

Only the agent's maker (the project's owner or a co-owner) can do this. Read first, then change:

1. `get` (`agent`, plus `crew_id` over MCP): whether the agent is a bot, its current audience, the functions it can
   offer (the project's functions, workflow functions included) and the Teams it can be shared with
   (`team_choices`).
2. `set` with `on=true` and what changes:
   - who can use it: `everyone=true` (anyone at the company), or `people` (user ids) and `teams` (team ids from
     `get`);
   - `functions`: the names other people's agents may call. A new bot starts with every offered function ticked
     (the server decides); send the list only to keep some back. Ask is always on and is not in the list;
   - `name` and `purpose`: one plain sentence on what it does and when to use it, for example "Reproduces a reported
     bug, files the ticket and hands the fix to the debugging bot";
   - picture: `icon` (one emoji), or `avatar_icon` with `avatar_color`. A photo is set from the bot card in the app.
3. `set` with `on=false` stops it.

Ask the person who should use it before turning it on; do not pick an audience for them. Read the result of `get`
back before saying it is done.

## What runs when someone uses it

- **Ask:** a private chat with the agent, as the caller, with the project's setup.
- **A function:** runs as the bot's agent (its name, purpose, instructions and model) in a fresh chat per call, with
  the function's typed inputs. A workflow function runs that workflow. The function's own caller list still decides,
  and the call and its cost are the caller's.

To add a function a bot can offer, define it first (`functions action=define`; read `panel-dashboard`'s functions
procedure), then tick it with `set`.

## Use someone else's bot

- `list`: bots shared with you and your own, each with its id, purpose and functions.
- `ask` (`bot`, `message`, optional `inbox_id` to continue) then `read` (`inbox_id`) for the answer.
- `call` (`bot`, `function`, `input`), then read the result with `functions action=status`.
- `add` / `remove` (`crew_id`, `bot`): put a bot into a project you build so its agents can ask it; it shows in that
  project's agent list and right menu. `list` with `crew_id` returns the bots added to it.

To make an agent in another project a bot, pass that project's `crew_id` (from `workspace action=list`) with `get`
and `set`; it works only when the person you act for made that agent. Otherwise only its maker can, so say who.
