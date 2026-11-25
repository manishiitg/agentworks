# Owners and users in Access

A project has one main owner (its creator). A project on the server also has:

- **Co-owners**: they build it like the owner and change everything (files, agents, memory, instructions, skills, settings, automations, sharing) and see every chat in it, while turns still run in the owner's server account.
- **Users**: for a project set up for someone to do specific work. A User works in it through chat with a co-owner's working power (commands, files, tools, functions), but sees only their chat and the project's dashboard, runs on the project's model, and cannot change its instructions, skills, settings, automations or sharing, nor the extra locked files the owner names. The server refuses those changes on every path (chat, their own AI app over MCP, the website).

A project on a Mac has its Team instead and neither.

Use `crew_owners` (list; add and remove for co-owners; user_add, user_remove and user_files for users) or the Access panel's Users and Co-owners sections, where the owner picks the person from the server's accounts. Only the main owner adds or removes people; anyone with a seat can list them. Over the tool, give the person's account email or user id; someone who has never signed in cannot be added yet. `user_files` sets a User's extra `locked_paths` (relative to the project); the instructions, skills and settings are always locked. Adding a User who is a co-owner moves them to Users, and the other way round.

Read the list before changing it and read it back afterward. Neither is Share: Share (Only me / Chosen people / Everyone) is who can open and ask the Crew in Run mode. An account administrator is not automatically the owner of someone else's private Crew.

A workflow is a Python script inside its Crew. Manage the Crew's access through the current tools; do not create a separate workflow owner list or conversation.
