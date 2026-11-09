# Working with a Team

Inspect the current Crew first. A Team is people working together on its shared
folder and chosen agents; it is not a separate workflow execution team. The Crew
has one Python workflow, not another owner list for each task.

The first-time Team setup chooses people and their level, the shared folder
(whole Crew or selected folder), and agents (Main selected initially), then
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
  on/off, whole Crew versus folders, and removing folders; joined members can
  add folders. Preserve unrelated folders. Show the returned change preview
  and follow the service's confirmation requirement.
- `agents` lists what teammates share. `agent_rule` changes only your own
  agent's instructions for others, change permission and chat visibility.
  Use the offered enums; the website calls chat visibility Live (empty) or Hidden (`agent`).
- `clashes` shows both versions when the same lines changed. Read the Crew
  builder's clash procedure before `resolve`; save the requested version or
  merge, never silently discard either person's work.

Connections, accounts and secret selections keep their own Vault rules. Team
membership alone does not copy all local files or sign-ins. A person's agent
and tab controls remain theirs. Read back the saved result and say what is
shared, with whom, and whether an invitation still waits for acceptance.
