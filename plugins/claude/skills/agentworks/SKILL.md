---
name: agentworks
description: Person commands for attaching this conversation to AgentWorks.
disable-model-invocation: true
---

The person command is /agentworks attach. The hook proposes a stable name for this exact conversation, then the person confirms with /agentworks attach --confirm or chooses /agentworks attach NAME. All account senders are accepted; sender allow flags are removed. The person can also use /agentworks resume, /agentworks cleanup, /agentworks detach or /agentworks status.

The prompt-submit hook consumes this literal command before it reaches the model. If these instructions appear in model context, the hook did not consume the command: do not execute it with a shell or tools. Ask the person to enable/reload the installed hook and submit the command again. The Claude namespaced spelling /agentworks:agentworks is also accepted.

Hooks deliver at turn boundaries. Attachment selects a verified native Claude session or Codex desktop route when available. Other native routes require explicit provider setup. Idle sessions without native wake notify the person. Nothing in this skill authorizes installation, attachment, hook-trust changes or terminal typing.

Session end retains the identity offline. Only the person can confirm /agentworks resume in that exact session. Cleanup keeps pending bodies and a receive watermark, preventing automatic replay; retired message metadata requires an explicit inbox read/reply. The daemon owns a restricted per-agent child connection; the skill cannot acquire the parent login.
