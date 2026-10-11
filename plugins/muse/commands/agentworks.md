---
description: Attach, detach or inspect this Muse conversation through the person's AgentWorks command.
argument-hint: attach NAME "PURPOSE" --allow-sender NAME | detach | status
---

The person requested `/agentworks $ARGUMENTS`.

Attachment is a person-only action. The UserPromptSubmit hook must consume the
literal command before it reaches the model. If you see this template, the hook
did not consume it: explain that the AgentWorks plugin/bridge must be installed
and its hooks approved, and ask the person to retry in the terminal. Do not run
attach, detach, installation, plugin approval or configuration through a tool.
Do not infer attachment from an available MCP connection.
