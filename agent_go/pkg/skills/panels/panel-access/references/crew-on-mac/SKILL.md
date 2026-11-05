# A Crew on this Mac, from chat

The Access page of a Crew on the owner's Mac has three tabs, and everything on it also works from that Crew's own
chat (the owner only, in the Mac app):

**Mac online** (only you, from anywhere, while this Mac is on): `manage_crew_sync`
- "Where does it live?" → `action=status`.
- "Use it from my phone / the web" → `action=reach_on` (with several networks, ask which and pass `network`).
  "Only on this Mac" → `action=reach_off`. Turning it off also turns the Team off.
- "Give me the link / use it from ChatGPT or Claude" → `action=links`. Signing in there stays the person's own click.
- AgentBridge (other people's agents): "make Writer public" → `action=public agent=Writer`; "only my agents" →
  `action=only_my_agents agent=Writer`. Public means agents can message it; it never shows on anyone's website.

**Publish** (move one agent, or the whole Crew, to run on the server from now on):
- One agent → `action=publish_preview agent=…`, show what moves, and after the person's yes `action=publish_agent`
  with the `transfer_id` and `confirmed=true`.
- Whole Crew → tell the person it is one-way, get their yes, then `action=server confirmed=true`.

**Team** (work on this Crew with people):
- "Turn on the Team" → `action=team on=true` (it also turns Mac online on). Off → `on=false`.
- People and what the team shares → `team_sync configure`. A person added is invited and joins when they accept; every
  member reads and writes what is shared. `share=folders folders=[…]` shares one or more of this Crew's folders
  (a new Team starts with an empty `team/`); `share=whole` shares the Whole Crew, everything except chats, memory, app
  files, `node_modules`/`.git`/caches and files that look like passwords or keys. Say its size first (the Team card
  shows it); no confirm. Switching back to folders stops syncing the rest; copies already on teammates' Macs stay.
  Teammates get it as one folder with the Crew's layout (~/AgentWorks/<Crew>/ on a Mac, an attached folder for their
  agents). Sync history and clashes → `team_sync recent` / `clashes`; sort a clash out with
  `team_sync resolve` (keep=current, keep=other, or merged `content`).
- Each agent is **In team** (everyone in the Team sees it and its chat live; their agents can message it) or
  **Only me**: `action=share agent=…` / `action=unshare agent=…`. "All my agents" → `action=team_all on=true`.
  Main is In team by default. A purpose is optional (`sessions action=identity` in its tab sets one).
- By default a shared agent only answers questions; pass `can_make_changes=true` when the owner wants it to work for them.
- Files that look like they hold passwords or keys are skipped by the sync and stay on this Mac. When the person asks to share one anyway, `action=sync_anyway path=<exact path from the skipped list> on=true`; it then goes to everyone in the Team. `on=false` takes it back (removed from the shared folder, kept on this Mac).

Say "On this Mac", "Mac online", "Publish" and "Team"; never relay, twin or copy.
