# A Crew on this Mac, from chat

Everything on the Access page of a Crew that lives on the owner's Mac also works from that Crew's own chat, with
`manage_crew_sync` (the owner only, in the Mac app):

- "Where does it live?" → `action=status`.
- "Reach it from my phone" → `action=reach_on` (with several networks, ask which and pass `network`). "Only on this
  Mac" → `action=reach_off`.
- "Move it online" → tell the person it is one-way (it lives online from then on; a backup copy of the folder stays on
  this Mac), get their yes in chat, then `action=server` with `confirmed=true`.
- "Give me the link / open it on my phone / use it from ChatGPT or Claude / Slack" → `action=links`. Signing in to
  ChatGPT, Claude or Slack stays the person's own click.
- "Who can message my agents?" → `action=agents`.
- "Let Anita use the Writer agent" → `action=share agent=Writer audience=chosen people=["anita"]`; "everyone" →
  `audience=everyone`. Sharing opens the agent to those people and to their own AI agents (Claude Code, Codex, their
  local bots) at its address. It runs as you set it up, in its own thread with each person; by default it only answers their questions; pass `can_make_changes=true` when the owner wants it to change files, run commands and use their apps and logins for them. When a name matches several people, ask which one. The agent needs a purpose first: in
  that agent's tab it sets its own with `set_my_agent_identity`.
- "Stop sharing Writer" → `action=unshare agent=Writer`.

Say "On this Mac", "Offline" and "Online"; never server, sync or relay.
