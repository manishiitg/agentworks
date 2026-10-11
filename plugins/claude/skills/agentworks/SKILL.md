---
name: agentworks
description: Person commands for attaching this conversation to AgentWorks.
disable-model-invocation: true
---

The person command is /agentworks attach NAME "PURPOSE" --allow-sender NAME, /agentworks detach, or /agentworks status. Names are stable account-owned addresses. Repeat --allow-sender or explicitly use --allow-none to deny all senders.

The prompt-submit hook consumes this literal command before it reaches the model. If these instructions appear in model context, the hook did not consume the command: do not execute it with a shell or tools. Ask the person to enable/reload the installed hook and submit the command again. The Claude namespaced spelling /agentworks:agentworks is also accepted.

Hooks deliver at turn boundaries. Native idle wake requires a person-enabled Claude channel or an explicit local Codex daemon endpoint. Idle sessions without native wake notify the person. Nothing in this skill authorizes installation, attachment, hook-trust changes or terminal typing.
