# Instructions and identity

Read current project identity and instructions first. Use workspace and the project’s scoped instruction-file tools for its identity and instructions. Use settings for its offered connection, knowledge-link, secret-name and notification fields. Use models for model choices. Preserve unrelated settings and verify the saved result. Discover the current schema; never edit provider secrets or managed runtime configuration files directly.


## App chat instructions

Use `settings` action=get to read the optional `chat_instructions` on the project. When its owner asks, set it with action=update and `chat_instructions` (empty clears; omitted preserves). The same field appears as **Chat instructions** beside Purpose in Settings. Keep it under 4,000 characters: useful behavior, tone and what to ask first, in plain words. It is visible to people who can use the app; never store secrets here. It is shared app guidance composed with Purpose and platform policy, not a complete system prompt override, tool grant or replacement for project instruction files. Existing permissions and confirmation rules still apply. Verify the saved value; a changed instruction refreshes retained chat guidance on the next turn.

Define workflow inputs, defaults and presets in script.json through the canonical workflow file/input tools; read python-workflows first. Agents list under their project, forks under the agent they came from; the person's own agent groups are arranged with `sessions action=group` and forks with `sessions action=move_out` (read crew-agents). Project pins and the Projects page filter (All, Mine, Shared with me, location) are set by the person in the app; there is no tool for them. Model choices use `models action=get/set`, with an offered `scope` of project or tab.
