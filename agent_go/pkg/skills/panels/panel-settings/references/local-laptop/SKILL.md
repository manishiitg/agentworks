# Local laptop

In Crew and Goal chats these resources live together in the Vault panel.
Use panel-access for the panel inventory, project-use ticks and chat routing;
this procedure covers the underlying operation.

Goals and Crews use terminal: devices/get first, browse to find a folder, set for the complete saved choice or remove. All Builders of a project share its saved connection and grants. The devices reply gives install and connect steps when needed. External MCP passes workflow_id for a Goal or workspace_id for a Crew, exactly one. Read access and Read & write are different from the separate commands switch. Commands run as the person with their own environment, files and logins when they choose it. Never expose sign-in files or send server secrets to the laptop. Verify saved state; new access applies from the next message.

Use terminal action=local_files, terminal action=local_write, terminal action=local_copy (local_path to project_path) and terminal action=local_execute for the connected computer. Normal files and shell stay on the server. Laptop tools are available only in a person’s interactive Builder chat or own MCP connection, never steps, schedules, AgentWorks, child agents or Run callers.

The one-time computer connection is in Integrations → Local laptop → Connect a computer. Builder chat uses vault action=mine_laptops to see your computers and setup steps without choosing a project. External MCP uses terminal action=laptop_devices without a project selector for the same personal list. Then use terminal in the chosen Goal or Crew for its saved selection and grants. Connecting in Integrations → Local laptop → Connect a computer never gives workflow steps a laptop; Gmail and Slack selections for steps remain independent.
