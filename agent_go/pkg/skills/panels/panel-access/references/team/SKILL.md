# Working with a Team

Inspect the current Crew first. A Team is people working together on its shared
folder and chosen agents; it is not a separate workflow execution team. The Crew
has one Python workflow, not another owner list for each task.

Team dashboards run from items explicitly added to the Team's online Crew. There is no periodic read-copy snapshot from a member's computer; publishing or updating an item never reseeds the Team's live data. Use the ordinary online-item list and receive-online action, not a Mac snapshot as source or export authority.

Teammates' In-team agents are reached only through AgentBridge at the exact `agent:` address returned by discovery; there is no separate in-app Team messaging route or `team:` target. Current membership and In team are checked on every use. Use MCP `messages action=ask` (in Crew chat, `messages action=send` with `ask=true`) for a question: an offline agent's ask goes to the existing Team agent, or refuses if none is available. Direct `messages action=send` waits for the original agent and is never redirected. Read and reply in the original inbox. Never provision a substitute or use the maker's login. There is no automatic standby copy or handback. Member agents are listed from their own Mac pairs, not server-shadow chats in the owner's website copy. A Mac agent's own turns stay on its Mac; explicitly moved-online agents keep using their normal server home.

The first-time Team setup chooses people and their level, the chosen shared folders, and agents (Main selected initially), then
saves with Finish. If sharing requires a dashboard, explain that before saving
and ask before creating one. Do not create it as a side effect of listing.

Use `team_sync` in Crew chat or discover `crew_team_sync` over MCP. Their action
names differ: chat `status` lists Teams and invitations; MCP uses `invites`.
Chat changes folders/Team settings with `configure`; MCP uses `settings`.
Chat uses its returned `project`; MCP uses `crew_id`. Read the current schema
rather than copying action names or old workflow-team policy fields:

- `directory` finds eligible people; `people` reads their current levels.
  **Works with me** invites someone to join; **Dashboards only** is the Crew
  owner's choice on a server Crew. Moving between levels or removing someone
  uses that same People service. Show any returned gain/loss preview and apply
  its required confirmation only after the person agrees.
- Chat `status` / MCP `invites` lists the caller’s own invitations. Both tools
  admit `accept` and `decline`, using the returned chat `project` or MCP `crew_id`.
  Answer only from the person’s own chat. An invitation is not proof they joined.
  Chat `leave` and **Leave this Team** let members leave.
- Chat `configure` / MCP `settings` controls Team on/off and shared folders. The main owner controls
  on/off and removing folders; joined members can add folders. `folders` is the
  complete selection, with at least one folder: include existing folders to keep
  them. Other Crew files are not shared. People changes still use their own
  returned preview and confirmation requirement.
- `agents` lists what teammates share. `agent_rule` changes only your own
  agent's instructions for others, change permission and chat visibility.
  Use the offered enums: Answers only is the private default for terminal agents; Live shares the native conversation only when the person chooses it.
- `clashes` shows both versions when the same lines changed. Read the Crew
  builder's clash procedure before `resolve`; save the requested version or
  merge, never silently discard either person's work. Team history is private
  server state, separate from the Crew database: use these tools rather than
  trying to read or edit it through project files or database tools. Recent history
  keeps at least 30 days or 500 changes, plus current-file bases and both sides
  of open clashes. If an older copy needs to catch up, refresh Team sync first;
  old arbitrary versions can be unavailable even while a clash's saved sides remain.

How the Team works (apps and secrets): `way` lists what the Team shares, `way_add`
(item `app` or `secret`, id) is **Add to Team** for one of the person's own, and
`way_remove` takes one off; every Team member may do all three, on the server.
The login type decides, never the person. An app with no login or an API key,
and a secret, are copied once into the Team's vault inside the Vault service: the
person keeps theirs, the value is never shown to you or in chat, and the Team
(its agent and online routines) uses the copy. An app with a personal sign-in is
never copied: it is listed as "Team uses GitHub: each person signs in
themselves", and each member connects their own. Say which of the two happened.
The first Add to Team makes the Team's online home. Rules and skills are already
shared through the Team's setup; do not copy them again.
Members' agents on the server pick up the Team's shared apps and secrets while they work in that Crew. An agent
running on a member's Mac does not get them: the Team's keys stay on the server and are never copied to a laptop.
When work on a Mac needs the Team's keys, say "This needs the Team's keys; I'll ask the Team agent to do it" and hand
the task to the Team agent in plain words (the normal ask path), then report what it answers.
A key that lives only in a member's Mac Vault can still reach the Team: the Team agent tells the member's agent which
key names it needs (names only, in an agent message), and the member's agent runs way_add with item secret and that
name on the Mac. The Mac reads the value from its own Vault and sends it straight to the server; never ask for, read
out, paste or message a key's value, and report the name only.

Connections, accounts and secret selections keep their own Vault rules. Team
membership alone does not copy all local files or sign-ins. A person's agent
and tab controls remain theirs. Read back the saved result and say what is
shared, with whom, and whether an invitation still waits for acceptance.

The agent menu's "How this works" explains an agent, the team, who is on it and
what teammates can use, from this Crew's real agents and people (the server
words it, differently for the owner and for a teammate). An agent on the server
shows "Runs on the server, always available". When asked how teams work, say
the same: a team is the people the owner adds in Share, then Team; teammates
use only the agents the owner shares.

Accepting a Team invite follows where the person is. In the Mac app there is one action, "Add to my Mac": the shared
folder syncs to that Mac, through the server the invite was listed from (a server the person already added). On the
website there is one action, "Join", and the team works on the server. Decline is in both. The server says whether
the Crew can be added to a Mac (`can_add_to_mac`: the Team shares a folder); when it cannot, the Mac app says so and
offers only Decline.

## Receiving an Add to Team hand-off

When another member's local agent asks the Team agent to take something online,
use the normal agent-message conversation. Work out the dependencies together;
do not show the person a table picker, options window or checkbox. Keep the
technical preview between the agents and give the person a plain result, or a
question only when a required connection or decision is genuinely missing.

- Read the source agent's current `receive_online` preview: the published item,
  its declared data, functions, assets and the schedules or Watchers that feed
  it. Check what the Team Crew can actually run with its normal tools, accounts
  and selected secret names. Ask the source agent to prepare missing dependencies
  and preview again; do not guess IDs or say an unavailable dependency will work.
- Agree which feeding routines must move so the page can keep updating online.
  The source agent chooses their IDs from the preview's dependencies. Buttons
  must use the Team agent or an admitted online target, never a Mac-only chat.
  Personal sign-ins are not copied or lent to the Team agent. If Team keys are
  needed, discuss names only and use the existing Team Vault procedure; never
  put values, login files or credentials in messages or item packages.
- The **original member's source agent** calls `receive_online` preview and
  apply from that member's own authorized connection, with the current
  `item_revision`, stable `submission_id` and agreed `include_automations`.
  The Team agent must not impersonate that member or call as the source owner.
  Messages are coordination, not permission or a substitute for server checks.
  Do not copy files with shell commands or edit the server's mapping records.
- The backend creates the normal Team home on the first apply, not on an ask or
  preview. First acceptance copies the used data once; updating replaces code,
  never the live Team data. The backend moves selected schedules, pauses their
  source copy and confirms the online copy; do not stop source routines yourself.
- Read the apply result before reporting back. A preview, agreement or sent
  message is not acceptance. On refusal, return its plain reason and the next
  preparation step. After acceptance, report what is on the Team and whether
  the selected routines moved. Claim it keeps updating only after the normal
  online run is confirmed; keep any untested run clearly untested.
