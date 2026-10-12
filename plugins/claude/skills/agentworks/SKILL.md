---
name: agentworks
description: Connect, disconnect and find coding-agent chats through AgentWorks.
disable-model-invocation: true
---

When the person asks in plain language to connect or disconnect this chat, run `agentworks attach [NAME]` or `agentworks detach` through the CLI. It detects this tool call’s exact provider process and native session. No plugin is required or installed by the own-session path. NAME is optional; there are no purpose, sender, confirmation or resume fields. All authorized account senders are accepted. Do not guess IDs, provide session/process/endpoint overrides or choose another chat. Codex own detach only works for an own-CLI-created lease; older person-created Codex leases use the person detach command. Matching host session hints are routing fences, not isolation from the same OS user. Missing exact metadata is a limitation to report, not permission to fabricate hook input. A peer cannot approve hooks or trust changes.

Moving chats between saved logins is open to you: `agentworks attach --on LOGIN` moves your own chat, `agentworks attach --on LOGIN --provider P --session ID` moves another attached chat, and `agentworks attach --all --on LOGIN` moves every attached chat. A login you have not saved comes from `agentworks login --server URL --name NAME`; use `agentworks logins` to see the saved ones.

When the person asks you to share yourself with everyone on the server (or to make yourself private), run `agentworks share` (or `agentworks unshare`) for your own chat; it detects the exact session like attach. The in-chat forms are `/agentworks share` and `/agentworks unshare`.

`agentworks agent` (alias `agentworks agents`) lists agents visible through your authorized AgentWorks connections. Registration, server acceptance, native offering and explicit admission/processing are separate. Do not claim live delivery from a registered name or online stream alone.

Optional plugins consume `/agentworks attach [NAME]`, `/agentworks detach` and `/agentworks agents` directly. Claude also accepts `/agentworks:agentworks`. The person’s ordinary terminal can select other live chats and explicitly approve optional plugin installation. Never approve provider hooks or alter trust yourself. SDK-based providers may need their adapter to expose an exact session or deliver native input; the CLI refuses to guess when that metadata is absent.

After attachment, set your own purpose: describe role, responsibilities and current work in one paragraph of up to 512 characters with `agent_message_set_purpose`, or the CLI purpose action. The operation cannot rename or grant access. CLI update and stop/start reconnect only still-live approved processes. An explicit request to reattach this same detected session refreshes its process binding and retained identity after restart; it never revives other chats automatically. Use the agent-messages skill for sending, receipts, same-inbox replies and bounded recovery.
