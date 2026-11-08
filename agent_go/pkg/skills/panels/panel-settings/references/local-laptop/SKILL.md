# Local laptop

Use the Crew's Local laptop entry for its selected computer, folders and command access. Read panel-access for the current panel inventory and crew-laptop for the execution and account rules. A connected computer does not sync the Crew's files.

Use terminal devices/get first, browse to find a folder, then set the complete saved choice or remove it when requested. The devices reply gives install and connect instructions. External MCP selects the Crew with workspace_id. Read access and Read & write are separate from the command switch. Commands use the connected person's environment, files and logins; never expose sign-in files or send server secrets to the laptop. Read back the saved selection and grants before reporting success.

Use terminal action=local_files, local_write, local_copy (local_path to project_path) and local_execute only where offered for that Crew. Normal server files and shell remain separate. The backend's saved folder, command, caller and connection checks decide whether a particular turn can use the computer; follow crew-laptop rather than inferring access from a visible connection.

Connect a computer once through Integrations → Local laptop → Connect a computer. In Crew chat, vault action=mine_laptops returns the person's computers and setup instructions without selecting a project. External MCP uses terminal action=laptop_devices for the same personal list. Then choose the computer and grants in the intended Crew. Connecting a computer does not give a workflow's Python script laptop access; its selected apps remain separate.
