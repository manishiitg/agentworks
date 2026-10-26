# Instructions and identity

Read current project identity and instructions first. Crew uses workspace plus its scoped instruction file tools; Goals and Relays use settings action=get/update and workspace for label/description/icon. Use settings for its offered connection, knowledge-link, secret-name and notification fields. Use models for model choices. Preserve unrelated settings and verify the saved result. Discover the current schema; never edit provider secrets or managed runtime configuration files directly.


## App chat instructions

Use `settings` action=get to read the optional `chat_instructions` on a Goal, Crew or Relay. When its owner asks, set it with action=update and `chat_instructions` (empty clears; omitted preserves). The same field appears as **Chat instructions** beside Purpose in Settings. Keep it under 4,000 characters: useful behavior, tone and what to ask first, in plain words. It is visible to people who can use the app; never store secrets here. It is shared app guidance composed with Purpose and platform policy, not a complete system prompt override, tool grant or replacement for Builder instruction files. Existing permissions and confirmation rules still apply. Verify the saved value; a changed instruction refreshes retained chat guidance on the next turn.

Use `settings action=variables` only where offered to change declared workflow variables. `settings action=chat` reads or changes the personal Context display; it does not change project access. Model choices use `models action=get/set`, with an offered `scope` of project or tab.
