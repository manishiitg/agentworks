---
name: agentworks
description: Use AgentWorks workflows, Crews, Relays, dashboards, Brain and Vault through the tools and guidance allowed by the current MCP connection.
---

# AgentWorks

Use this as an entry pointer. The server owns the current procedures and permissions.

1. Discover the connection's tools with `get_api_spec` (remote MCP) or the live tool list. Get schemas before guessing arguments; invoke only listed tools/actions through `call_tool` or the stdio tool itself.
2. Call `help action=context` for current consent, project roles and `guidance_version`. Compare it with the version you saved at the start of this task; refresh after a change. A reference does not grant permission.
3. Call `help action=topics` or `help action=skills` for names, descriptions and per-topic versions. Load only relevant topics with `help action=topic`, passing `topic` and your saved `known_version`. An unchanged response has no body. Preserve the returned `external_note` alongside content: it maps internal reference operations to this connection's actual tools.
4. To install selected reference skills, use `help action=skills` with `topics` and `known_versions`. Write returned changed `files` under your client skill folder as `<topic>/<path>`. Never replace a customized skill silently. The default catalog contains paths and versions, not every topic's text.
5. Discover projects through `workflow action=list` and `crew action=list`; IDs are not filesystem paths. Load `crew-authoring` for Crew work, `relay-builder`/`relay-dashboard` for Relay work, and the listed Builder reference topics for workflow work. Topic lists are filtered by current connection access.

Messages use `messages action=send/read` and `inbox_id`; receiving agents decide whether to reply. Declared functions use `functions action=list/call/status/read/reply`, with `call_id` and a stable `submission_id` for identical retries. `functions action=calls` lists Crew calls; owner status/read replies can contain `commands_run`. Read the current schemas and topic before using these details.

Direct file edits use current roles, existing authoring consent, revisions and file guards. Direct workflow plan tools and some shared Crew/Relay tools remain unavailable; use `builder action=chat` to delegate only when it is listed. Never assume a tool exists because an internal reference mentions it. Server bridge environment, secret values and internal URLs do not belong in external client commands.

For CLI users, `agentworks guidance context/topics/topic/skills` provides the same discovery. Server tool commands check the guidance version at startup and report changes on stderr; stdout stays JSON/MCP-only. A client that never fetches guidance cannot receive updates automatically.
