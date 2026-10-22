---
name: panel-models
description: Models panel in Goals and Crews. Inspect offered models and change the saved model choice.
metadata:
  panel-tools:
    work:
    - manage_crew_model
    - settings
    agentworks:
    - get_llm_config
    - list_provider_models
    - list_published_llms
    - set_workflow_llm_config
    - settings
    relays:
    - get_llm_config
    - list_provider_models
    - list_published_llms
    - set_workflow_llm_config
    - settings
  external-tools:
  - manage_crew_chats
  - get_llm_config
---

# Models

Crew uses manage_crew_model list/set for this chat; an empty engine restores its Crew default. Use settings to change the Crew default itself, preserving its other model roles. Goals and Relays use get_llm_config, list_provider_models/list_published_llms, set_workflow_llm_config or settings for its role models. Select only an offered provider/account/model/reasoning level. A saved change applies from the next message; do not claim this current answer already uses the new model. Read back the saved choice.

For configuration details and runtime effects, Read [the detailed procedure](references/configuration.md).
