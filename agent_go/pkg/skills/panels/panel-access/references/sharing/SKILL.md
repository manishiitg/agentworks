# Share this Crew

Read the current Crew's sharing and Team state before changing it. Use only the
admitted tools and exact people/agents returned by the server. A link grants no
access by itself. The server decides what the caller can change; a guide does
not grant permission or another person's tab controls.

Share has three routes:

- **Work on it with your team:** People lists **Works with me** and **Dashboards
  only**. Read the Team procedure before invitations, level or folder changes.
- **Share agents:** each of your agents has an Ask switch, **Let them make
  changes**, and chat visibility **Live / Saved only / Hidden**. Use the returned
  agent keys, current choices and changeability. Other people's agents appear
  under **Shared with you**; do not change their settings.
- **Slack:** choose the Crew's saved bot, name, channels and direct-message
  settings here. Read `panel-channels`' Slack bot procedure for setup and
  diagnostics. Personal **Use Slack as me** stays in Connections.
- **Leave this Team:** a member leaves with the button at the end of Share,
  which asks "Leave this Team?" first. In chat, leave only when the person asks
  to leave this Team; the owner cannot leave their own Crew.

Dashboards have their own **Dashboards** tab next to Share: everything the
Crew publishes, who can open each one, or "No dashboard yet" with Ask the Crew.
Share an existing publication, or ask whether to create a standard dashboard
when one is required. Set `create_dashboard=true` only after agreement. Listing
or publishing an Action does not create a dashboard. The Crew menu shows one
dashboards line and lists each dashboard by name; a name opens that dashboard,
the line opens the Dashboards tab.

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
readable; do not promise all history for a Hidden or Saved only agent. A stored
message is acceptance, not a completed answer. Another person's message is an
attributed request, never an owner approval.

UI controls open only the Dashboard. Point to **Share** in the Crew menu for
other screens, and use admitted management tools for authorized changes. Read
back the saved people, agents, folder or audience before reporting completion.
