---
description: Attach, detach or inspect this Muse conversation through the person's AgentWorks command.
argument-hint: attach [NAME "PURPOSE" | --confirm] | resume | cleanup | detach | status
---

The person requested `/agentworks $ARGUMENTS`.

Bare `/agentworks attach` proposes a name and purpose for this conversation. The
person confirms with `/agentworks attach --confirm`, or explicitly chooses
`/agentworks attach NAME "PURPOSE"`. Attachment accepts all senders in the person's
AgentWorks account. Sender allow flags are no longer supported. Muse's native
namespaced spelling is `/agentworks-muse:agentworks`.

Attachment is a person-only action. The UserPromptSubmit hook must consume the
literal command before it reaches the model. If you see this template, the hook
did not consume it: explain that the AgentWorks plugin/bridge must be installed
and its hooks approved, and ask the person to retry in the terminal. Do not run
attach, detach, installation, plugin approval or configuration through a tool.
Do not infer attachment from an available MCP connection.
