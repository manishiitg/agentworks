# Team and Online

Read the current project's sharing and Team state before changing it. Use only the
admitted tools and exact people/agents returned by the server. A link grants no
access by itself. The server decides what the caller can change; a guide does
not grant permission or another person's tab controls.

## A project on the server

Sharing is three separate things; the right menu shows them as **People**, **Bots** and **Dashboard**:

- **People** work *in* the project: co-owners build it with the main owner, Users work in it through chat with its
  setup (locked files). Use `crew_owners` and read `references/owners/SKILL.md`.
- **Bots** are its agents that others *use* without joining, each in their own private chat, with the functions the
  maker ticks. Use `bots` and read `references/bots/SKILL.md`.
- **Dashboard** is shared by its link, like a Drive file: people added plus general access (`share_dashboard` with
  `access` restricted or company, and `emails`; a public copy for anyone with the link is the main owner's
  `set_dashboard_public_link`). Someone with only the dashboard opens its pages and their data, never the project,
  its chats, agents or files, and gets no Ask or function buttons. To let people ask or use the project without
  joining, share an agent as a bot.

## A project on a Mac

The project menu has two rows:

- **Team:** the people who work on this project with you. People lists **Works with
  me** and **Dashboards only**, then the Team's agents and the shared folder.
  Read the Team procedure before invitations, level or folder changes.
- **Online:** how everyone else reaches the project, in tabs: **Phone** (with
  AgentBridge) on a Mac project, **Share agents**, **Slack** and **Dashboards**.

- **Share agents** (an Online tab): each of your agents is one card: its name and where it runs,
  then **Share <agent> with your team**. When that is on, it helps teammates as it
  helps you, with no limits unless you write a rule (**Your instructions for
  teammates**), and **Your chat** is **Live** or **Hidden**. **Instruction for others** and
  **Permissions…** open its rules. Use the returned agent keys,
  current choices and changeability. Other people's agents appear under **Shared
  with you**; do not change their settings.
- **Leave this Team:** a member leaves with the button at the end of Team,
  which asks "Leave this Team?" first. In chat, leave only when the person asks
  to leave this Team; the owner cannot leave their own project.

Slack has its own **Slack** tab under Online (shown only where Slack bots are
allowed): the project's saved bot, its name, channels and direct-message settings,
and **Who can talk to it** (a channel @mention answers as the project's owner in a
thread; people from other workspaces or whose Slack email is not an AgentWorks
account are refused). Read `panel-channels`' Slack bot procedure for setup and
diagnostics. Personal **Use Slack as me** stays in Connections.

Dashboards have their own **Dashboards** tab under Online: everything the
Project publishes in three kinds (Dashboards, Artifacts, Motion, each shown even at
0 with a one-line hint and, for the owner, Ask the project), who can open each one,
or "No dashboard yet" with Ask the project.
Share an existing publication, or ask whether to create a standard dashboard
when one is required. Set `create_dashboard=true` only after agreement. Listing
or publishing an Action does not create a dashboard. The project menu's Online
row has a **Dashboards** header (how many, and who can open them) that opens and closes
the list of dashboards by name; a name opens that dashboard, and **Manage
dashboards** opens the Dashboards tab.

There is one **Actions** group for the whole project, separate from agent Ask
switches. An Action either **Starts the workflow** with typed inputs and saved
settings, or **Asks the agent <name>**. Inspect its **Who can ask** and saved
on/off setting. A declared Action runs with its owner's configured access; that
access is not given to its caller. Free-text Ask runs as the attributed caller
under the existing Run rules. Read `panel-dashboard` before creating Actions.

Respect the server's fixed controls: the last shared agent cannot be turned off
while sharing requires one. Explain
the returned reason rather than bypassing it through another API. Audience and
agent selection are rechecked for reads, sends and queued delivery.

For MCP, discover selected agents through `workspace action=chats` or
`messages action=list_agents`; use the returned target and chat_id for
`read_chat`, `ask` or `messages action=send`. Chat visibility controls what is
readable; do not promise any history of a Hidden agent. A stored
message is acceptance, not a completed answer. Another person's message is an
attributed request, never an owner approval.

UI controls open only the Dashboard. Point to **Team** or **Online** in the project
menu for other screens, and use admitted management tools for authorized changes. Read
back the saved people, agents, folder or audience before reporting completion.

The person's own AgentWorks project (the front-door agent every account has) is never shared. Every share path
refuses it with "Your AgentWorks project stays private; share another project instead.": Team and people, Run share,
dashboard links, and the Slack bot. Its Access page shows that one line and no tabs. Say the same and suggest sharing
another project; do not look for another way around it.
