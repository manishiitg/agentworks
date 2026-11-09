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

AgentBridge publication (`public` / `only_my_agents` with the agent's name) is
separate from website sharing. Public means other agents on that server can find
and message it; it does not put the Crew on everyone's website. Do this only on
the person's request, preserving current server checks.

## Move execution to the server

For one agent, use `publish_preview` with its name and selected relative paths.
Show what moves, size, destination account and missing setup. After the person
agrees to that concrete preview, use `publish_agent` with the same transfer_id and
confirmed=true. Other agents stay on the Mac. A whole-Crew `server` move is
one-way: explain it and get the person's yes before confirmed=true.

Inspect the returned provider and continuation mode. Native history, file copy,
credentials and available server tools are separate; never promise every CLI or
account can resume identically. Missing server sign-in uses its existing Providers
flow, not copied local credential files. A receipt or source save is not live
publication; verify the actual returned state and link.

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
rules. Use `team_sync agent_rule` for what others may ask and what chat they see.
There is no `team_all` action in this tool; do not invent one.

Inspect `lent` for this Mac's selected apps/secret names unavailable to teammates.
Use a returned item's explicit copy/move action only when the person requests it;
Team does not silently copy all Vault sign-ins. Secret-looking files skipped by
sync remain local. `sync_anyway` requires the person's exact requested skipped
path and means sharing it with everyone in the Team; never enable it as a repair
for a missing credential. Never print secret values.
