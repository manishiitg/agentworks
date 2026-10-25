---
name: panel-models
description: Models panel in Goals and Crews. Inspect offered models and change the saved model choice.
metadata:
  panel-tools:
    work:
    - manage_crew_model
    - settings
    - chat_preferences
    agentworks:
    - get_llm_config
    - list_provider_models
    - list_published_llms
    - set_workflow_llm_config
    - settings
    - chat_preferences
    relays:
    - get_llm_config
    - list_provider_models
    - list_published_llms
    - set_workflow_llm_config
    - settings
    - chat_preferences
  external-tools:
  - manage_crew_chats
  - get_llm_config
  - chat_preferences
---

# Models

Crew uses manage_crew_model list/set for this chat, including connection_id for your personal or available company account; an empty engine restores its Crew default. Use settings to change the Crew default itself, preserving its other model roles. Goals and Relays use get_llm_config, list_provider_models/list_published_llms, set_workflow_llm_config or settings for its role models. Select only an offered provider/account/model/reasoning level. A saved change applies from the next message; do not claim this current answer already uses the new model. Read back the saved choice.

For configuration details and runtime effects, Read [the detailed procedure](references/configuration.md).

Context usage appears beside the composer model by default when the runtime reports a figure. Models has one personal display toggle. Use `chat_preferences` with no arguments to read it, or `show_context_indicator: true/false` to change it for the person asking across their chats. This does not change the project or model. An unknown figure stays hidden; never estimate one from the conversation length.
