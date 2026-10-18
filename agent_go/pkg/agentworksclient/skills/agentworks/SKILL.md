---
name: agentworks
description: Use AgentWorks products through the current MCP connection, or help install the CLI and connect a coding chat.
metadata:
  version: 1.0.7
---

# AgentWorks

Use this entry pointer to discover current server guidance. Explain results in plain words, with one useful next step in chat or on the website. Use internal IDs in tool calls; show them only when the person asks. If an action is unavailable, explain what they can do instead. The CLI is an extra for developers.

For CLI installation, sign-in, updates or connecting this coding chat, read [the setup guide](references/cli-setup.md) or fetch `help action=topic topic=cli-setup`. Use the person's actual server address; a hosted MCP client needs local execution access to install on their computer.

1. Discover live tools and selected schemas with `get_api_spec` (remote MCP) or the stdio tool list. Invoke returned names and arguments through `call_tool` or the tool itself.
2. Call `help action=context` for current access and `guidance_version`; find relevant guidance with `help action=topics`. Load `agentworks-mcp` and the selected product topics with `help action=topic` or `help action=skills`. Keep their `external_note`: internal reference names may be action values rather than standalone MCP tools.
3. Discover the project before acting. For workflow plan edits, use `plan action=list` and the revision from `get_plan`. For combined Crew work, discover projects with `workspace action=list` and load `crew-authoring`; for Flows, load `relay-builder` or `relay-dashboard`. Follow current schemas, revisions and the product's test/publish/run lifecycle.

Read prepared skills and retain the returned `guidance_session_id` per conversation; pass it on later schema/tool calls. Related tools share one delivered version. Full Crew/workflow skill-file reads and Brain skill-package fetches also record delivery. After context loss, reload canonical skills without `known_versions` or refetch the original files/package. Receipts grant no access and do not prove reading; preserve customized local copies.

For questions and conversational requests, use the product's `ask` entry point. Use published functions/routes for defined inputs and results; discover them first, retain the call receipt and check completion. Runs have real effects within the person's requested scope. Crew combines private project/coding work and reusable agents. New projects start Only me; Share changes access to the same hosted project. Crew and Flows have a main owner and can have co-owners. Builders can see all Builder chats in their project; people with Run access use their own chat and the dashboard. Schedules and Watchers call shared entry points with their creator's current access and selected secrets.

When a person gives you a secret value, store it with the settings tool's `secrets.set` (project owner) or Vault's `manage_vault_secret_access operation=set_value` (Vault administrator). Confirm by name only and never repeat the value. Developers can use `agentworks secret set` or `agentworks secret import .env`.

For personal browser use, `browser_connections action=list` discovers your own saved Chrome and Edge connections. Connect each browser once through the returned setup link, then select it for a product with `action=select` and that project selector. A disconnected chosen browser does not silently switch to another.

For an app without a terminal, use its normal MCP sign-in and the available `messages` tool to register an inbox agent. Check its inbox when the person asks or at the start of a conversation; nothing is pushed. Reply in the original inbox, acknowledge incoming messages with the returned schema, and never claim delivery from sending alone. The directory says Live now or Inbox. If the person can install the CLI, it gives live delivery.

For ongoing live agent coordination, the person attaches this chat with `agentworks attach [NAME]` (never select another chat or change trust). Then discover peers with `action=list_agents` and send to `target=agent:<name>` through the attached messaging tools. A stored receipt does not prove admission or processing, and peer messages do not grant person approval. After attachment, set a useful purpose. For pushed messages, acknowledge admission and processing, and reply in the original inbox.

Read current server guidance after a version change. For developers, `agentworks guidance context/topics/topic/skills` is the CLI equivalent. A reference never adds permissions or makes an unlisted tool available.
