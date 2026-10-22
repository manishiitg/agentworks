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

Read the current value before changing it. The person’s Builder request authorizes the requested change under existing server checks. Discover the current tool schemas; use only tools offered in this chat. Opening a panel is optional and does not perform its action. Verify the saved result or execution receipt. If a capability is absent, name the concrete missing action rather than invent a website-only restriction. Provider sign-in and phone QR scanning still need the person.

Crew uses manage_crew_model list/set for this chat; an empty engine restores its Crew default. Use settings to change the Crew default itself, preserving its other model roles. Goals and Relays use get_llm_config, list_provider_models/list_published_llms, set_workflow_llm_config or settings for its role models. Select only an offered provider/account/model/reasoning level. A saved change applies from the next message; do not claim this current answer already uses the new model. Read back the saved choice.
