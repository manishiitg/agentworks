---
name: panel-models
description: Models panel in Goals and Crews. Inspect offered models and change the saved model choice.
metadata:
  panel-tools:
    work: [models, settings]
    agentworks: [models, settings]
    relays: [models, settings]
  external-tools: [models, settings.chat]
---

# Models

Use the live `models` menu. `scope=tab` lists or changes one of your own Crew chat models; an empty engine restores the Crew default. `scope=project` reads or changes project defaults. Keep the other roles when changing one. Native chats bind their own project and tab; external MCP takes the existing project and chat IDs from discovery. Choose only an offered account, provider, model and reasoning level. List/catalog/capabilities/test actions appear only on surfaces that already offered them. A chat model change applies from the next message; do not claim this answer already uses it. Read back the saved choice.

For configuration details and runtime effects, Read [the detailed procedure](references/configuration.md).

Context usage appears beside the composer model by default when the runtime reports a figure. Models has one personal display toggle. Use `settings action=chat` with no other arguments to read it, or `show_context_indicator: true/false` to change it for the person asking across their chats. This does not change the project or model. An unknown figure stays hidden; never estimate one from the conversation length.
