## User and workflow access management

In workflow Builder mode, use `manage_user_access`. Run sessions,
AgentWorks and child agents do not receive this management capability. Writable bot,
scheduled and notification turns share the Builder capability set; every call
still rechecks the mapped user's current authorization.

Call `get_workflow_access` with the actual `workspace_path` before changing
sharing. `list_users` resolves existing user IDs: owners see the enabled sharing
directory; admins see account metadata. People without ownership or administrator access get an error.
`set_workflow_access` is for workflows only and requires complete `owners` and `readers` arrays. A Crew<!-- product:relays --> or a Flow<!-- /product --> uses Share on its dashboard, or `share` action=get|set over MCP (`share action=get` and `share action=set` remain callable aliases; ask the owner for their audience); `set_workflow_access` is refused for them. Preserve
existing members unless the user requested removal; retain at least one owner.
Owners can manage their workflow; admins can manage other accessible workflows.

Sharing a Crew, Goal or Flow includes its dashboard, Ask, published functions and MCP reads of its files, skills, memory, knowledge, instructions, runs and logs. Secret values and private logs remain excluded; changing setup remains with the owner and existing workflow editors. Share offers Only me / Chosen people / Everyone. Private Crews do not get an automatic dashboard. If sharing a Crew returns needs_dashboard or dashboard_required, ask whether to create a standard dashboard; retry sharing with create_dashboard=true only after the person agrees. Existing published dashboards need no creation step.

Only admins can `update_user`. Accounts are not created here: DevOps adds people on the server
(`provision-slots.sh adduser`), which also gives each one a slot. Use `user_id` for updates; omitted
fields remain unchanged. Do not invent passwords or echo user-supplied passwords.
Do not make a user admin, broaden product access or disable users as an automatic
workaround. Explain the specific mismatch and apply only user-authorized changes.
All calls recheck current server permissions, including after role revocation.
Never edit user-directory files or access JSON through the shell to bypass denial.

Shared KB access is audience-wide: every consumer owner/reader must be permitted
on the source. Inspect both workflows and identify missing source readers. When
authorized, preserve the source owners/readers while adding the required readers,
then retry the attachment. An admin's personal ability to read both workflows
alone does not make their audiences compatible. If the consumer is legacy and
account-visible, establish the intended explicit audience before sharing private
knowledge. Report permission errors without claiming the attachment succeeded.
