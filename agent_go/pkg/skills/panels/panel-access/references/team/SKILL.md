# Working with a Team

Inspect the current Crew first. A Team is people working together on its shared
folder and chosen agents; it is not a separate workflow execution team. The Crew
has one Python workflow, not another owner list for each task.

Team dashboards run from items explicitly added to the Team's online Crew. There is no periodic read-copy snapshot from a member's computer; publishing or updating an item never reseeds the Team's live data. Use the ordinary online-item list and receive-online action, not a Mac snapshot as source or export authority.

A teammate's agent opens in the main chat with one header line (back, the agent, whose computer and its state, Fork) and an **Ask <agent>** box at the bottom. The box sends an AgentBridge ask from you to that agent's address; its answer shows under the box. The conversation above stays for watching. A hidden chat keeps the same layout with a private note, and you can still ask.

Teammates' shared agents are reached only through AgentBridge at the exact `agent:` address returned by discovery; there is no separate in-app Team messaging route or `team:` target. Current membership and Shared with team are checked on every use. Use MCP `messages action=ask` (in Crew chat, `messages action=send` with `ask=true`) for a question. An agent offline under 10 minutes is Away: the ask waits for it. Past that, nothing is sent and the reply says how long it has been offline; tell your person and let them choose: ask again with `offline="wait"` (it answers when it is back), or read its chat now with `messages action=history target=<the same address>` and `query="words"` (or `line=N` from a result; in Crew chat the `read_agent_chat` tool). Never choose for them. Reading works only while the agent is offline past 10 minutes and only for a chat its person shows as Live chat (Teammates see); Just the agent refuses. Say that what you read may be out of date. Nothing on the server answers as an offline agent. When the agent is back, it is told which teammates' agents read its chat and what they searched, and corrects them if needed. Direct `messages action=send` waits for the original agent and is never redirected. Read and reply in the original inbox. Never provision a substitute or use the maker's login. A shared agent's chat syncs to the server after each turn (joined Claude Code/Codex sessions on join and each turn-end hook, even with Only me or Team off); a large history may take later turns to finish. Same-session reconnect keeps its cursor; a different native session never inherits it. Removing the maker revokes sync and reads. Member agents are listed from their own Mac pairs, not server-shadow chats in the owner's website copy. A Mac agent's own turns stay on its Mac; explicitly moved-online agents keep using their normal server home.

The first-time Team setup chooses people and their level, the chosen shared folders, and agents (every agent ticked; untick any to keep private), then
saves with Finish. If sharing requires a dashboard, explain that before saving
and ask before creating one. Do not create it as a side effect of listing.

## Words people use

- **Team kit** (Team → Kit; formerly Team Todos): everything an agent needs to
  work the Team's way, in one list: the Team instructions (every agent follows
  them automatically), skills, tools and keys (apps and keys the Team holds,
  sign-ins and MCP tools each person adds), and setup steps (tools call them
  setups, such as "Connect your agent chat"). Each row shows who shared it and
  who has it. Each person's own agent does the steps and ticks them. A setup
  step is not a task.
- **Asks**: an agent asking another agent (`messages ask`) or a person
  (`person:<name>`, a Needs-you card). An ask is an AgentBridge thread; its reply
  comes back in the same inbox.
- **Tracking work** (what is planned, who is doing it, what was decided and why)
  lives in the project's own issue tracker named in its instructions (for
  example GitHub issues). The Team product keeps no task list.

## When the person says "check the Team kit" (or "the Team Todos")

1. Read the kit: `team_sync` `setup_kit` in Crew chat; over MCP (Claude Code,
   Codex) `workspace` action `team_setup_kit` with `crew_id` from
   `team_invites`. Each item's `mine` says what this person has (`missing` or
   `unknown` still needs work); `setup_list` lists just the setup steps.
2. For each one not finished, read it with `setup_get` (or `team_setup_get`):
   its instructions, item IDs, `setup_revision` and `instruction_revision`.
3. "Check" means report: per setup step, done/total, plus any missing skill,
   tool or sign-in, and the next step. "Do" or
   "complete" means carry out the steps on this person's own machine, verify
   each one, then record it with `setup_checklist` (current revisions,
   `done={item_id: true}`, plus `installed` for any MCP or skill the setup lists).
4. Tick only what you verified. A step only the person can confirm (they see a
   status line, they approved a prompt) stays unticked until they say so: ask
   them in one line. Applying or queueing is not done. Never tick for another
   person, and never ask for or show a key's value: keys are names only.
5. Reply with each step's done/total and what still needs the person.

## When the person says "what's on the Team" or "what's waiting"

1. Read `team_sync` `work_brief`. `my_tasks` are asks sent to you that you
   have not finished (each with `inbox_id` and `message_id`);
   `waiting_on_your_person` are requests your person must answer. In a Claude
   Code or Codex chat the same brief is the `agentworks` tool, action
   `team_sync`, `team_action=work_brief`.
2. Work and decisions are tracked in the project's own issue tracker (GitHub,
   Jira or files) named in its instructions: check it with your own tools.
3. Report in a few lines. Do not start work unless asked.

## Working with other agents

- Start every session with `work_brief` and finish the asks in `my_tasks`:
  reply in their inbox with `status=in_progress` while you work, then `done`
  (or `blocked`, `needs_you` when stuck).
- Hand work to another agent with `messages ask` (or `queue`); a plain `send` is
  only a notice.
- Record progress and decisions on the issue or ticket the work belongs to, with
  your own tools; the Team product keeps no tracker.
- Blocked on a person (a review, an approval): ask them as below. Carry on with
  other work meanwhile.

## Asking a person (a review, an approval, a question)

Your own person: just ask in this chat. Anyone else in the Team: send a request
to `person:<name>`, with the name as the Team panel shows it (a first name is
enough; use the full name when two people share it). It pins a
Needs-you card in their Team panel with its links and files, up to three
buttons and a note box. Their click, their note, or both come back to you in
that request's inbox.

- **Review a PR:** the PR link in `files`, a line on what to look at, buttons
  `Approve`, `Changes needed`, `Ask my agent`.
- **Review a plan or doc:** attach it as a `.md` file (it opens on the card),
  the same three buttons.
- **Approve a deploy or a decision:** say what happens on Approve; buttons
  `Approve`, `Not now`.
- **An open question:** ask it in one line with `Ask my agent` among the
  buttons; they can write the answer in the note, or hand it to their own
  agent, which replies to you in words.

Give the request the issue number. "Changes needed" usually comes with a note:
do what it says, then ask again. With no answer after a day, ask once more, or
tell your person.
"Ask Riya to review PR 3026" from your person means exactly this request.

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

Sharing a skill with the Team (#3459, optional both ways; nothing is installed for anyone
automatically). The Kit lists shared skills under Skills with "Add to my skills" and
"Share one of my skills", which send their request to the person's own agent.
To share: ask which skill if unclear; read its folder `skills/<name>/` (text files only:
SKILL.md and scripts; stop and say so if it has a binary file, more than 40 files or about
200 KB). Check that no key value is written in any file (names such as `$SECRET_DEPLOY_KEY`
are fine; a literal token or password is not: tell the person and leave the file out of
the share until it is removed). Then `team_sync` `save_file` `shared-skills/<new id>.json`
in the Team's shared folder holding `{"kind":"skill","name","description","files":{"SKILL.md":"…","scripts/deploy.sh":"…"}}`
(reuse your own earlier file and its base hash to update it). Keys a script needs are named
in SKILL.md by name only; values stay in the Team vault (`way_add` secret) and Team keys never
reach a Mac, so on a Mac hand a run that needs one to the Team agent. "Who may run it" is
guidance written in the skill, not a new permission.
To add a teammate's: `files` / `read_file` the `shared-skills/*.json`, then show the person
SKILL.md and every script in full, say which keys it names, and ask. Only after a yes, write
the files into the person's own `skills/<name>/` under that name; if they already have a skill
with it, ask for another name and never overwrite theirs. Say what was added. Stop sharing
deletes the shared file; copies already added stay.

Updating a shared command, schedule or theme (#3458; a shared skill the same way): copies are never linked to
the shared file, so you compare. When the person asks, when the Kit's "Ask my agent
to update" message arrives, and once at the start of a session when a quick look at
the Team's `commands/`, `schedules/` and `themes/` files shows a teammate's item
whose same-named copy the person has differs, do this:
1. Read the shared file (`files` / `read_file`) and the person's own copy.
2. Say in plain words what is different (a short before and after for text; for a
   schedule the new time and message), and who changed the shared one if the list shows it.
3. If both sides changed, merge them, show the merged text and say which lines are
   the person's own. Offer "theirs", "mine" or the merge; ask once.
4. Only after a yes, save it with the normal tool for that item (commands `update`,
   the schedules `update`, the appearance `import` under the same name) and say what changed in one line.
   Never update, turn on or delete anything silently. A schedule stays as it was
   (on or off) unless the person says otherwise.
5. Owner side ("update the shared review command"): compare the person's own item with
   the shared file, say what differs, and after a yes `save_file` the same file
   (reuse its path and base hash). Teammates' copies do not change; their own agents offer them the update.
A shared skill is updated the same way (compare the files, show changed script lines, ask once, then write them to the person's own `skills/<name>/`); for the owner, `save_file` the same shared file with its base hash.

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

The native plugin keeps `team_sync` for the session brief (`team_action=work_brief`). Asks, discovery, questions and replies use normal AgentBridge; The Team kit uses the AgentWorks MCP `workspace` actions `team_setup_*` when that chat has them. Asks to an offline agent follow the offline rule at the top of this guide. Joining grants no general Crew tool scope.


## Agent sharing card wording

The header and right panel use one agent sharing card. Its server-written summary explains the current saved choices; do not invent a separate permission summary. Labels are **Teammates can ask this agent**, **It may: Only answer / Answer and do the work**, **Who can ask: Everyone on the team / Only people I pick**, **Teammates see your chat: Yes, live / No**, **Your instructions for teammates**, and **Uses · See all**. Only answer may read, look things up and run checks, but changes nothing and sends nothing as the person. Answer and do the work can use the person's files, tools and sign-ins; it acts as them. Hiding the chat does not hide admitted replies to teammates. Examples expand on tap or hover. **Ask AI to explain** drafts in that agent's own chat, without sending or changing settings. Appearance may style the card, never hide its summary or controls or infer authority from them.

The compact **Team kit** row opens Team → **Kit**, the segment after Sync in the same Team strip. It lists the Team instructions, skills, tools and keys with who shared each and who has it, and each setup step's agent-checked progress; **Apply what's missing** and each step's Apply stay agent-led. Automatic join setup summaries are agent replies in chat, not a separate status record; show only actual checklist progress and live key-status results, never infer completion or pending work.
