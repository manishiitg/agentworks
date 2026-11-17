# A Crew on this Mac

Inspect `manage_crew_sync action=status` before answering where work runs. Use
only the current admitted actions from the owner's Mac app. Connecting a laptop,
syncing a shared folder, reaching the same local chat and publishing an agent are
different operations; do not claim one completed another.

## Reach it while this Mac is on

When asked to use it from phone/web, use `reach_on`; with several networks, use
the chosen `network` and ask only if ambiguous. `links` returns its website,
phone and external-client addresses. A link does not complete browser sign-in.
`reach_off` is refused while Team is on: explain that dependency and ask whether
the owner wants the Team off, rather than claiming it was disabled automatically.

AgentBridge Discovery (`public` / `only_my_agents` with the agent's name) is
separate from website sharing. Discovery on means other people's agents on that
server can find and message this agent; it does not put the Crew on everyone's website. Do this only on
the person's request, preserving current server checks.

## Keep work running while the Mac is off

Agents are not published or put online by hand (owner, 17 Nov). An agent that
is Shared with team keeps an online copy that answers teammates while this Mac
is off, automatically. Work that must keep running on its own (a report, a
check, a reminder) is an automation: put the schedule, Watcher or webhook
online through the Team agent. For anything Team-wide, ask the Team agent.
Only me agents answer only while this Mac is on.

## Work together

Read this panel's Team and Sharing procedures for People, shared folder, agent
Ask/change permission/chat visibility and the one Actions group. In chat,
`team_sync configure` changes folders/Team settings; `status` reads Teams and
invitations. Follow the returned preview and confirmation when switching whole
Crew versus folders. Membership is not complete until the invitation is accepted.
Whole-Crew sync still excludes protected app/chat/login files and ignored
installed packages, Git internals and caches; do not promise a complete disk copy.

The owner can use `manage_crew_sync team` for the Mac's Team switch. With several
networks, choose the requested connection before turning it on. Use its admitted
`share`/`unshare` actions for an own agent, respecting fixed Main/last-shared-agent
rules. Use `team_sync agent_rule` for Shared with team, who it is shared with (whole Team or chosen people), what others may ask and what chat they see.
There is no `team_all` action in this tool; do not invent one.

Inspect `lent` for this Mac's selected apps/secret names unavailable to teammates.
Use a returned item's explicit copy/move action only when the person requests it;
Team does not silently copy all Vault sign-ins. Secret-looking files skipped by
sync remain local. `sync_anyway` requires the person's exact requested skipped
path and means sharing it with everyone in the Team; never enable it as a repair
for a missing credential. Never print secret values.


## shared agents

Teammates' shared agents are reached only through AgentBridge at the exact `agent:` address returned by discovery; there is no separate in-app Team messaging route or `team:` target. Current membership and Shared with team are checked on every use. Use MCP `messages action=ask` (in Crew chat, `messages action=send` with `ask=true`) for a question: an offline agent's ask goes to the existing Team agent, or refuses if none is available. Direct `messages action=send` waits for the original agent and is never redirected. Read and reply in the original inbox.

Claude Code or Codex joins a Crew from the installed plugin Crew skill (Claude: `/agentworks:crew <Crew ID>`), or its `agentworks` tool `action=join` with `project=<Crew ID>`; explicit `action=leave` removes it. This exact session must already be connected. The person must own the Crew or be a current member of its active Team, not just have dashboard Run access. The server returns Crew instructions and an owner-admitted local folder or accepted Team copy; follow the instructions and work there. With no local copy it returns `copy=null`, `folders=[]`: the files stay on the server; ask its agents or add the Crew to this Mac in AgentWorks. Never supply or guess a server/local path or a native session. Keep the Mac app running for shared sync. A new join follows the Crew's Team: Shared with team, Answers only, no change permission when on; only the person and their own agents when off. Turning Team on later needs no rejoin; rejoin keeps Only me/Live choices. The owner can read their own exact terminal journal; other people's native text requires explicit Live. One terminal agent joins one Crew until it leaves. Saved Discovery stays saved but grants no audience while joined; leaving restores it. Team keys stay server-only.

Codex terminal delivery uses approved hooks at turn boundaries, not Claude’s idle live channel. For one explicit bounded wait in the exact attached Codex tool call, use `agentworks bridge message watch --provider codex --after 0`, then retain the returned next_cursor. It waits up to 20 seconds once and exits; never create a polling loop, approve hooks, or promise immediate idle wake.
