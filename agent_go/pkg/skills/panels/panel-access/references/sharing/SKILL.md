# Share this Crew

Read the current Crew's sharing and Team state before changing it. Use only the
admitted tools and exact people/agents returned by the server. A link grants no
access by itself. The server decides what the caller can change; a guide does
not grant permission or another person's tab controls.

Share has two routes (team, agents):

- **Work on it with your team:** People lists **Works with me** and **Dashboards
  only**. Read the Team procedure before invitations, level or folder changes.
- **Share agents:** each of your agents is one card: its name and where it runs,
  then **Share <agent> with your team**. When that is on, teammates either **Get
  answers** (the agent changes nothing) or **Get answers and make changes**, and
  **Can teammates see your own chat with <agent>?** is **Live** or **Hidden**.
  **Permissions for <agent>…** opens its rules. Use the returned agent keys,
  current choices and changeability. Other people's agents appear under **Shared
  with you**; do not change their settings.
- **Leave this Team:** a member leaves with the button at the end of Share,
  which asks "Leave this Team?" first. In chat, leave only when the person asks
  to leave this Team; the owner cannot leave their own Crew.

Slack has its own **Slack** tab next to Share (shown only where Slack bots are
allowed): the Crew's saved bot, its name, channels and direct-message settings,
and **Who can talk to it** (a channel @mention answers as the Crew's owner in a
thread; people from other workspaces or whose Slack email is not an AgentWorks
account are refused). Read `panel-channels`' Slack bot procedure for setup and
diagnostics. Personal **Use Slack as me** stays in Connections.

Dashboards have their own **Dashboards** tab next to Share: everything the
Crew publishes in three kinds (Dashboards, Artifacts, Motion, each shown even at
0 with a one-line hint and, for the owner, Ask the Crew), who can open each one,
or "No dashboard yet" with Ask the Crew.
Share an existing publication, or ask whether to create a standard dashboard
when one is required. Set `create_dashboard=true` only after agreement. Listing
or publishing an Action does not create a dashboard. The Crew menu has a
**Dashboards** header (how many, and who can open them) that opens and closes
the list of dashboards by name; a name opens that dashboard, and **Manage
dashboards** opens the Dashboards tab.

There is one **Actions** group for the whole Crew, separate from agent Ask
switches. An Action either **Starts the workflow** with typed inputs and saved
settings, or **Asks the agent <name>**. Inspect its **Who can ask** and saved
on/off setting. A declared Action runs with its owner's configured access; that
access is not given to its caller. Free-text Ask runs as the attributed caller
under the existing Run rules. Read `panel-dashboard` before creating Actions.

Respect the server's fixed controls: a published Main agent stays shared, and
the last shared agent cannot be turned off while sharing requires one. Explain
the returned reason rather than bypassing it through another API. Audience and
agent selection are rechecked for reads, sends and queued delivery.

For MCP, discover selected agents through `workspace action=chats` or
`messages action=list_agents`; use the returned target and chat_id for
`read_chat`, `ask` or `messages action=send`. Chat visibility controls what is
readable; do not promise any history of a Hidden agent. A stored
message is acceptance, not a completed answer. Another person's message is an
attributed request, never an owner approval.

UI controls open only the Dashboard. Point to **Share** in the Crew menu for
other screens, and use admitted management tools for authorized changes. Read
back the saved people, agents, folder or audience before reporting completion.

The person's own AgentWorks project (the front-door agent every account has) is never shared. Every share path
refuses it with "Your AgentWorks project stays private; share another Crew instead.": Team and people, Run share,
dashboard links, and the Slack bot. Its Access page shows that one line and no tabs. Say the same and suggest sharing
another Crew; do not look for another way around it.
