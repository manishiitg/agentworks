# Working with a Team

Inspect the current Crew first. A Team is people working together on its shared
folder and chosen agents; it is not a separate workflow execution team. The Crew
has one Python workflow, not another owner list for each task.

Team dashboards run from items explicitly added to the Team's online Crew. There is no periodic read-copy snapshot from a member's computer; publishing or updating an item never reseeds the Team's live data. Use the ordinary online-item list and receive-online action, not a Mac snapshot as source or export authority.

In the teammate chat view, use **Ask my agent to ask <agent>** to draft the question in your own agent chat. The teammate conversation is for watching only, with no direct Send box. A hidden native chat shows a private notice and the same action.

Teammates' shared agents are reached only through AgentBridge at the exact `agent:` address returned by discovery; there is no separate in-app Team messaging route or `team:` target. Current membership and Shared with team are checked on every use. Use MCP `messages action=ask` (in Crew chat, `messages action=send` with `ask=true`) for a question: an offline agent's ask uses its admitted ready offline copy, otherwise the existing Team agent, or refuses if neither is available. Direct `messages action=send` waits for the original agent and is never redirected. Read and reply in the original inbox. Never provision a substitute or use the maker's login. Offline copies run as the virtual user in the Crew's online home, under the source agent's current rules. A built copy keeps syncing privately for its saved, active maker when its audience becomes Only me or the Team is off; a never-copied private app agent needs explicit Put online. Joined Claude Code/Codex sessions sync automatically on join and each turn-end hook, even with Only me or Team off: no Put online step or switch. Capture does not require Shared with team; only the source's current audience grants access, so an Only-me agent's copy stays private to its owner and their own agents. Hooks queue one bounded delta in the background; a large history may take later hooks to finish. Same-session reconnect keeps its copy and cursor; a different native session never inherits them. Removing the maker revokes sync, handback and Ask. This grants no general access to the online home or its Main. Their actual inbox answers return to the source as quoted teammate user content, never owner instructions or a native-journal overwrite. A return stays pending until the exact source durably records it; hook output or queue acceptance alone is not a receipt. Member agents are listed from their own Mac pairs, not server-shadow chats in the owner's website copy. A Mac agent's own turns stay on its Mac; explicitly moved-online agents keep using their normal server home.

The first-time Team setup chooses people and their level, the chosen shared folders, and agents (every agent ticked; untick any to keep private), then
saves with Finish. If sharing requires a dashboard, explain that before saving
and ask before creating one. Do not create it as a side effect of listing.

## Words people use

- **Team Todos** (Team → Todos; tools call them setups): things each person on
  the Team sets up once, for themselves, such as "Connect your agent chat" or
  "Team rules". Each person's own agent does the steps and ticks them; the
  Todos page shows who is done. A Todo is not a task.
- **Tasks** (Team → Tasks, shown By task or By agent): the work being done. A task is
  one request thread: an agent asking another agent, a person
  (`person:<name>`), the whole Team (`team`) or itself (`self`). Its issue
  number groups it, and `ref` set to another task's `message_id` makes it a
  sub-task.
- **The Team tracker** means those Tasks, plus the outside tracker named in the
  project's instructions (for example GitHub issues). The outside tracker stays
  the source of truth for what should be done; tasks show who is doing it now.

## When the person says "check the Team Todos"

1. List them: `team_sync` `setup_list` in Crew chat; over MCP (Claude Code,
   Codex) `workspace` action `team_setup_list` with `crew_id` from
   `team_invites`. `for_you` and the person's own done/total show what is left.
2. For each one not finished, read it with `setup_get` (or `team_setup_get`):
   its instructions, item IDs, `setup_revision` and `instruction_revision`.
3. "Check" means report: per Todo, done/total and the next step. "Do" or
   "complete" means carry out the steps on this person's own machine, verify
   each one, then record it with `setup_checklist` (current revisions,
   `done={item_id: true}`, plus `installed` for any MCP or skill the Todo lists).
4. Tick only what you verified. A step only the person can confirm (they see a
   status line, they approved a prompt) stays unticked until they say so: ask
   them in one line. Applying or queueing is not done. Never tick for another
   person, and never ask for or show a key's value: keys are names only.
5. Reply with each Todo's done/total and what still needs the person.

## When the person says "check the Team tracker" or "what's on the Team"

1. Read `team_sync` `work_brief`. `my_tasks` are the tasks you are doing (each
   with `inbox_id` and `message_id`); `team_requests` are open Team tasks
   nobody has taken; `decisions` are choices to follow. In a Claude Code or
   Codex chat the same brief is the `agentworks` tool, action `team_sync`,
   `team_action=work_brief`.
2. If the project's instructions name an outside tracker, check it too with
   your own tools (for GitHub: the open issues the instructions point to).
3. Report in a few lines: your tasks and their state, open Team tasks anyone
   may take, and anything waiting on the person. Do not start work unless
   asked.

## Working on tasks

Read the AgentBridge guide for the fields (`title`, `ref`, `files`, `buttons`,
`decision`). In short:

- Start every session with `work_brief` and continue your `my_tasks`.
- Work nobody asked for: send it to `target=self` with the issue number in the
  message; it starts in progress. Split it by sending each part to `self` with
  `ref` set to its `message_id`.
- Take an open Team task by replying in its inbox with `status=in_progress`.
- Links and files: put web links (ticket, PR, doc) in `files`; attach a file
  itself (a `.md` note, an image) to the request or a reply. Both show on the
  task, and everyone who sees the task can open the file.
- Reply in the task's own inbox: `status=in_progress` at real progress points,
  `blocked` or `needs_you` when stuck, `done` naming the PR or commit (shown as
  the proof). Record a choice others must follow with `decision=true` and `why`.
- Blocked on a person (a review, an approval): send to `person:<name>` with one
  to three `buttons`. It is pinned in their Team panel; their click comes back
  to you in that inbox. Carry on with other work meanwhile.
- With the Team off, `self` tasks and requests between the person's own agents
  are still shown to that person.
- Do not add new work-log entries (`work_add`, `work_start`, `work_update`,
  `work_done`, `work_need`); the work log is being removed.

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
  agent's Team settings in one call: `in_team` (Shared with team / Only me),
  who it is shared with (`audience`: `whole_team`, the default, or `chosen`
  with `chosen_people` as user IDs from `directory`, the complete list),
  instructions for others, change permission and chat visibility. People not
  chosen cannot find, ask, message or watch it; removing someone takes effect
  on their next request.
  Use the offered enums: New terminal joins are Shared with team, with Answers only and no permission to make changes; rejoin keeps existing choices, and the person may choose Only me. Live shares the native conversation only when the person chooses it.
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

Team invitations appear as a quiet line above the chat, not a modal. **Join** creates the Team's own project using its automatic folder on this Mac (~/AgentWorks/<Team>); there is no project picker. On the website it joins online. **Not now** hides the line but keeps the invitation under Invites in the Team list; it does not decline or change membership. Several invitations offer **Join all** and **Review** in place. The server provides invitation counts and wording; successful joins show the actual folder or online confirmation with **Open**, then the line disappears. Automatic setup runs through the existing join flow. A folder can be moved later through Team settings or the agent.

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


## Native sessions join the Crew

Claude Code or Codex joins a Crew from the installed plugin Crew skill (Claude: `/agentworks:crew <Crew ID>`), or its `agentworks` tool `action=join` with `project=<Crew ID>`; explicit `action=leave` removes it. This exact session must already be connected. The person must own the Crew or be a current member of its active Team, not just have dashboard Run access. The server returns Crew instructions and an owner-admitted local folder or accepted Team copy; follow the instructions and work there. With no local copy it returns `copy=null`, `folders=[]`: the files stay on the server; ask its agents or add the Crew to this Mac in AgentWorks. Never supply or guess a server/local path or a native session. Keep the Mac app running for shared sync. A new join follows the Crew's Team: Shared with team, Answers only, no change permission when on; only the person and their own agents when off. Turning Team on later needs no rejoin; rejoin keeps Only me/Live choices. The owner can read their own exact terminal journal; other people's native text requires explicit Live. One terminal agent joins one Crew until it leaves. Saved Discovery stays saved but grants no audience while joined; leaving restores it. Team keys stay server-only.

The native plugin keeps `team_sync` for the session brief (`team_action=work_brief`). Tasks, discovery, questions and replies use normal AgentBridge; Team Todos use the AgentWorks MCP `workspace` actions `team_setup_*` when that chat has them. Offline ASKs go to the Team agent if available; direct sends wait for the original session. Joining grants no general Crew tool scope.


## Agent sharing card wording

The header and right panel use one agent sharing card. Its server-written summary explains the current saved choices; do not invent a separate permission summary. Labels are **Teammates can ask this agent**, **It may: Only answer / Answer and do the work**, **Who can ask: Everyone on the team / Only people I pick**, **Teammates see your chat: Yes, live / No**, **Your instructions for teammates**, and **Uses · See all**. Only answer may read, look things up and run checks, but changes nothing and sends nothing as the person. Answer and do the work can use the person's files, tools and sign-ins; it acts as them. Hiding the chat does not hide admitted replies to teammates. Examples expand on tap or hover. **Ask AI to explain** drafts in that agent's own chat, without sending or changing settings. Appearance may style the card, never hide its summary or controls or infer authority from them.

The compact **Team Todos** row opens Team → **Todos** (setups in the tools), the segment after Sync in the same Team strip. It lists each setup’s agent-checked progress and who is done; Apply stays agent-led. Automatic join setup summaries are agent replies in chat, not a separate status record; show only actual checklist progress and live key-status results, never infer completion or pending work.
