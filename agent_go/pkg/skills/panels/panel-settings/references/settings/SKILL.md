# Instructions and identity

Read current project identity and instructions first. Use workspace and the Crew’s scoped instruction-file tools for its identity and instructions. Use settings for its offered connection, knowledge-link, secret-name and notification fields. Use models for model choices. Preserve unrelated settings and verify the saved result. Discover the current schema; never edit provider secrets or managed runtime configuration files directly.


## App chat instructions

Use `settings` action=get to read the optional `chat_instructions` on the Crew. When its owner asks, set it with action=update and `chat_instructions` (empty clears; omitted preserves). The same field appears as **Chat instructions** beside Purpose in Settings. Keep it under 4,000 characters: useful behavior, tone and what to ask first, in plain words. It is visible to people who can use the app; never store secrets here. It is shared app guidance composed with Purpose and platform policy, not a complete system prompt override, tool grant or replacement for Crew instruction files. Existing permissions and confirmation rules still apply. Verify the saved value; a changed instruction refreshes retained chat guidance on the next turn.

Define workflow inputs, defaults and presets in script.json through the canonical workflow file/input tools; read python-workflows first. `settings action=chat` reads or changes the personal Context display; it does not change project access. `menu_groups` reads or changes the person's own left menu groups: list, create (name, optional projects), rename, delete, add or remove (group, project), naming Crews as the left menu shows them. Model choices use `models action=get/set`, with an offered `scope` of project or tab.
