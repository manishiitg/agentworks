---
name: agentworks
description: Explain person-controlled AgentWorks attachment and messaging status
---

AgentWorks control prompts must be consumed by beforeSubmitPrompt before model
execution. If this command reaches the model, the delivery hook is unavailable.
Tell the person to check installation and hook trust using the plugin README.
Do not run attach, detach, install, or synthesize a prompt-submit hook. The person
can use `/agentworks status`, `/agentworks detach`, or the explicit attach syntax
documented in the README after enabling the hooks.
