# Working with a Team

Inspect the current Crew first. A Team is people working together on its shared
folder and chosen agents; it is not a separate workflow execution team. The Crew
has one Python workflow, not another owner list for each task.

The first-time Team setup chooses people and their level, the shared folder
(whole Crew or selected folder), and agents (Main selected initially), then
saves with Finish. If sharing requires a dashboard, explain that before saving
and ask before creating one. Do not create it as a side effect of listing.

Use `team_sync` in Crew chat or discover `crew_team_sync` over MCP. Read the
current schema rather than translating old workflow-team policy fields:

- `directory` finds eligible people; `people` reads their current levels.
  **Works with me** invites someone to join; **Dashboards only** is the Crew
  owner's choice on a server Crew. Moving between levels or removing someone
  uses that same People service. Show any returned gain/loss preview and apply
  its required confirmation only after the person agrees.
- `invites`, `accept` and `decline` handle the caller's own invitations. An
  invitation is not proof they joined. Members use **Leave this Team** to leave.
- `settings` controls Team on/off and shared folders. The main owner controls
  on/off, whole Crew versus folders, and removing folders; joined members can
  add folders. Preserve unrelated folders. Show the returned change preview
  and follow the service's confirmation requirement.
- `agents` lists what teammates share. `agent_rule` changes only your own
  agent's instructions for others, change permission and chat visibility.
  Use the offered enums; the website calls visibility Live, Saved only, Hidden.
- `clashes` shows both versions when the same lines changed. Read the Crew
  builder's clash procedure before `resolve`; save the requested version or
  merge, never silently discard either person's work.

Connections, accounts and secret selections keep their own Vault rules. Team
membership alone does not copy all local files or sign-ins. A person's agent
and tab controls remain theirs. Read back the saved result and say what is
shared, with whom, and whether an invitation still waits for acceptance.
