---
name: agentworks
description: Explain person-controlled AgentWorks attachment and messaging status
---

AgentWorks control prompts must be consumed by beforeSubmitPrompt before model
execution. If this command reaches the model, the delivery hook is unavailable.
Tell the person to check installation and hook trust using the plugin README.
Do not run attach, detach, install, or synthesize a prompt-submit hook. The person
can use `/agentworks status` and `/agentworks agents` for read-only checks, or
`/agentworks detach` for an attachment-fenced detach. `/agentworks attach [NAME]`
is explicitly refused in this preview until shared PLAT-861 Cursor binding exists.
Never switch providers or bypass that refusal.
