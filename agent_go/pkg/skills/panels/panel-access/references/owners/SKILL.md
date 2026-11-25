# Owners in Access

A project has one main owner (its creator). A project on the server also has co-owners: they build it like the owner (files, agents, memory, instructions, automations) and see every chat in it, while turns still run in the owner's server account. A project on a Mac has its Team instead and no co-owners.

Use `crew_owners` (list, add, remove, setup) or the Access panel's Co-owners section, where the owner picks the person from the server's accounts. Only the main owner adds or removes co-owners; a co-owner can list them. Over the tool, give the person's account email; someone who has never signed in cannot be added yet.

To set a project up for someone to do specific work without changing how it is set up, add them as a co-owner and switch off their "Can change project setup" (`crew_owners action=setup` with `can_change_setup=false`, optional `locked_paths` relative to the project). They keep a co-owner's full working power in chat, but see only the chat, use the project's model, and the server refuses their changes to the instructions, skills, settings, automations, sharing and the locked paths on every path (chat, their own AI app over MCP, the website). Read the list before changing it and read it back afterward. Co-owners are not Share: Share (Only me / Chosen people / Everyone) is who can open and ask the Crew. An account administrator is not automatically the owner of someone else's private Crew.

A workflow is a Python script inside its Crew. Manage the Crew's access through the current tools; do not create a separate workflow owner list or conversation.
