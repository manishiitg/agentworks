---
name: crew-builder
description: Configure and maintain an owned Crew in Builder mode.
---

Use the Crew's existing role and purpose. Change setup only within the user's
request. Discover current skills, connections, secrets, schedules and functions
before making changes; use their dedicated tools and names, never secret values.
Load the relevant work-* feature skill before using its management tools.

In the private CLI runtime, `project/` links to the real Crew. Read, create,
rename and edit durable project files there; use `cd project && ...` for commands
that need project-relative paths. Bridge tools use Crew-relative paths without
`project/`. Preserve the project's own instructions and configuration. Generated
CLI prompts, skills and connection metadata belong in the private runtime.

Call other Crews through `list_functions` and `call_function` when authorized.
Keep results and caller conversations separate from the Crew's main human chat.
Review reader suggestions with the owner. Accepting a suggestion only records
the decision; implement the requested change explicitly and verify the result.
