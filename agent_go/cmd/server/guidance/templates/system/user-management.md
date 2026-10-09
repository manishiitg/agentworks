## User and workflow access management

In workflow Builder mode, use `manage_user_access`. Read-only Run sessions,
Pulse and child agents do not receive this management capability. Writable bot,
scheduled and notification turns share the Builder capability set; every call
still rechecks the mapped user's current authorization.

Call `get_workflow_access` with the actual `workspace_path` before changing
sharing. `list_users` resolves existing user IDs: owners see the enabled sharing
directory; admins see account metadata. Ordinary readers/non-owners get an error.
`set_workflow_access` requires complete `owners` and `readers` arrays. Preserve
existing members unless the user requested removal; retain at least one owner.
Owners can manage their workflow; admins can manage other accessible workflows.

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
