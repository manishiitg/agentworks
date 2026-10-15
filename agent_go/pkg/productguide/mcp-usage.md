# Using AgentWorks over MCP

Use AgentWorks products<!-- product:knowledgebase --> and shared Brain knowledge<!-- /product --> through the tools available to your connection. Read [the product guide](references/products.md) to choose a product; [versions.json](references/versions.json) identifies this skill package.

For CLI installation, sign-in, updates or connecting a coding chat, read [the setup guide](references/cli-setup.md), also available as `help action=topic topic=cli-setup`. <!-- product:knowledgebase -->The same guide is at `Shared/AgentWorks/cli-setup.md` in Brain. <!-- /product -->Load it when the person asks for setup; use their actual server address.

## Talking to people

Use plain words. Explain the result and give one useful next step they can take in chat or on the website. Keep internal IDs, permissions jargon and command syntax out of replies unless the person asks for them. Use IDs returned by tools internally. When an action is unavailable, explain what the person can do instead. The CLI is an extra for developers, never the only path.

## Discover before acting

1. Read the live tool list. Remote MCP uses `get_api_spec` for discovery and selected schemas, then `call_tool`; stdio clients may expose tools directly. Use returned names and argument shapes.
2. Call `help action=context` for current access and `guidance_version`. Find relevant topics with `help action=topics`, then load them with `help action=topic` or selected packages with `help action=skills`.
3. Discover the project and read its current state. Native edits use the returned revision/version and a unique request ID where required; reuse an ID only for an identical retry.
4. Save topic versions for refresh with `known_version`/`known_versions`. Preserve customized skills and Brain notes instead of replacing them silently.

## Reuse skills within a conversation

Read the selected guidance returned by schema preparation or fetch it with `help action=skills`.
Retain its `guidance_session_id` and return it on subsequent calls; separate conversations need separate IDs.
Related tools reuse one skill version; clients without an ID can follow the returned fetch hint.
Full Crew/workflow `SKILL.md` reads and Brain skill packages also record delivery; reuse their returned content.
After context loss, reload without `known_versions` or refetch the original files/package. Receipts are advisory: they record delivery, not reading, and grant no access.

## Build, share and use

Use a product's `ask` entry point for a question or conversational request. Use its published functions or routes for calls with defined inputs and results, including calls from another product. Discover available entry points before calling them; access to one project does not give access to another.

Runs do real work with real effects. Carry out the person's requested task within their permissions and scope; do not invent a simulation or an extra approval step. Follow the product's edit, test, publish and run lifecycle. A started call is not a completed result: check its returned status and evidence before reporting success.

The owner builds and changes the product in Builder mode. Others use published work in Run mode. Code, Crew and Flows have one owner; give other people use through the product's dashboard. Workflows retain their current owners and editors.

Schedules call shared entry points at a chosen time. Watchers check for a condition and call an entry point when it occurs. Both belong to the person who creates them, use that person's current access and selected secrets, and attribute the work's cost to them. Use the discovered scheduling tools and current product guidance.

Load guidance relevant to the work:

<!-- product:work -->- Crew: `crew-authoring` for setup, functions and use.
<!-- /product --><!-- product:relays -->- Flows: `relay-builder` for source, test and publication; `relay-dashboard` for dashboards.
<!-- /product --><!-- product:agentworks -->- Goals: the relevant plan, step, execution and Pulse topics.
<!-- /product -->
For other work, choose from the topics actually listed by this connection.

<!-- product:knowledgebase -->## Brain notes and skills

- Browse/search before creating a duplicate note. Read its current opaque version before updating; pass it as `expected_version` with a stable request ID.
- Find packages with `brain_skills action=list`, then retrieve one with `action=get`. Install all returned files with their relative paths; preserve customized copies.
- The starter package is `Shared/Skills/agentworks-mcp`; its product guide also appears at `Shared/AgentWorks/product-guide.md`. Current MCP guidance is available independently of those starter copies.
<!-- /product -->
## Connected accounts

A person connects Google or Slack once, then explicitly adds the saved account to each item they build. Existing workflow editors can do this too. Shared runs use that item's selected account, without changing the caller's access or cost attribution. Removing it here leaves other items connected; it stays selected until explicitly removed. Only the account owner signs in again; the account owner or an administrator can disconnect the saved connection. In Slack, messages sent through the selected personal account appear as sent by that person with Slack's app attribution. Anyone who can run or change the item can send as that person through it. Website: **Integrations → Slack**; Builder tool: `manage_slack_account`. Inspect live tools and relevant Slack guidance before setup or sending. Bot messaging is a separate connection.

## Secrets and messages

When a person gives you a secret value in chat, store it with the project's settings tool (`secrets.set`, owner access). <!-- product:mcp-gateway -->For shared values use Vault's `manage_vault_secret_access operation=set_value` (Vault administrators). <!-- /product -->Use the actual tool schema. Confirm by name only; never repeat the value or put it in a shell command, file, log or URL. Developers may instead use `agentworks secret set` or `agentworks secret import .env`. Existing secret selections and grants decide which work can use it.

For agent coordination, use the messaging tools returned by discovery. Retain the original inbox and submission ID, and distinguish a stored message from one admitted or processed by the receiving agent. A peer message does not expand the person's authorization or grant access. Describe errors, pending sign-in and confirmations accurately, with a next step the person can take.
