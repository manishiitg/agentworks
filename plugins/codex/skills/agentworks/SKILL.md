---
name: agentworks
description: Person commands for attaching this conversation to AgentWorks.
disable-model-invocation: true
---

The person commands are `/agentworks attach [NAME]`, `/agentworks detach` and `/agentworks agents` (plus help). Typing attach is approval: it connects or reconnects this exact chat, preserves its saved identity after restart, and discovers its native route. NAME is optional; there are no purpose, sender, confirmation, resume or cleanup options. All account senders are accepted. In a terminal, `agentworks attach [NAME]` installs the supported plugin if needed and lets the person choose a verified live session.

The prompt-submit hook consumes literal commands before they reach the model. If this skill appears in model context, the hook did not consume the command: do not execute attach/detach with a shell or tools. Ask the person to reload/review the installed hooks and type the command again. Claude's namespaced `/agentworks:agentworks` spelling is accepted.

After the person's attach, local setup context asks the agent to describe its purpose and provides a sanitized roster. Use `agent_message_set_purpose` for a one-line purpose of up to 80 characters. This cannot attach, rename or grant permissions. Native delivery can wake the exact attached chat; hooks deliver at turn boundaries when no native route is available. Only the person attaches or detaches. Never change host trust or permissions.

CLI update and stop/start retain existing identities and reconnect still-live approved processes automatically. If the coding-agent process itself restarted, the person types attach again to approve its new process. Cleanup is automatic; pending bodies and receive watermarks survive, and ambiguous offers never replay automatically. Stored messages can be recovered with an explicit bounded inbox read. The daemon uses restricted per-agent child connections and never gives the plugin the parent login.
