# Using AgentWorks over MCP

Use AgentWorks products<!-- product:knowledgebase --> and shared Brain knowledge<!-- /product --> through the tools available to your connection. Read [the product guide](references/products.md) to choose a product; [versions.json](references/versions.json) identifies this skill package.

For CLI installation, sign-in, updates or connecting a coding chat, read [the setup guide](references/cli-setup.md), also available as `help action=topic topic=cli-setup`. <!-- product:knowledgebase -->The same guide is at `Shared/AgentWorks/cli-setup.md` in Brain. <!-- /product -->Load it when the person asks for setup; use their actual server address.

The website's local AI setup shows optional terminal-app installation, then connecting the AI app to MCP tools. <!-- product:bullpen -->Optional live messaging between coding chats follows through `agentworks setup` for the selected app and **AgentBridge → Connect an agent**. <!-- /product -->MCP tools do not require the AgentWorks terminal app.

## Talking to people

Use plain words. Explain the result and give one useful next step they can take in chat or on the website. Keep internal IDs, permissions jargon and command syntax out of replies unless the person asks for them. Use IDs returned by tools internally. When an action is unavailable, explain what the person can do instead. The CLI is an extra for developers, never the only path.

## Discover before acting

Your own MCP sign-in has your website access. Owners and workflow editors build; people using shared products use Run mode. Do not ask people to choose extra tool permissions for their own sign-in. Machine, service-account and device connections retain their assigned limits.

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

Use a product's `ask` entry point for a question or conversational request. Use its published functions or routes for calls with defined inputs and results, including calls from another product. Flows use simple inline agents/classifiers; use a Goal when work needs both Crew and Flow functions. Discover available entry points before calling them; access to one project does not give access to another.

Runs do real work with real effects. Carry out the person's requested task within their permissions and scope; do not invent a simulation or an extra approval step. Follow the product's edit, test, publish and run lifecycle. A started call is not a completed result: check its returned status and evidence before reporting success.

Crew combines the former Code and Crew products. Use `workspace action=list` to discover projects of either origin and keep the returned IDs. New projects start Only me; Share changes access to the same hosted project. Builders, including co-owners, can see all Builder chats in their project. Other people use their own chat, the dashboard and published functions in Run mode. Crews and Flows have a main owner and can have co-owners. Workflows retain their current owners and editors.

To manage a product's sharing, use `share` action=get to inspect its audience, then action=set with `crew_id`, `workflow_id` or `flow_id` and the owner's choice of `private`, `people` (with `emails`) or `everyone`. Preserve existing people unless the owner requested removal. Shared people open the dashboard, Ask, use published functions and read product files, runs and logs over MCP. Secret values and private logs stay excluded. Sharing creates a standard dashboard when needed.

Schedules call shared entry points at a chosen time. Watchers check for a condition and call an entry point when it occurs. Both belong to the person who creates them, use that person's current access and selected secrets, and attribute the work's cost to them. Use the discovered scheduling tools and current product guidance.

Load guidance relevant to the work:

<!-- product:crew -->- Crew: `workspace` for discovery, project setup and chat tabs; `crew-authoring` for detailed setup, functions and use. Crew works on the server. Optional laptop tools use the owner’s selected computer and folder access from Crew Settings; they do not replace server work or create a separate published project.
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

A person connects Google or Slack once, then explicitly adds the saved account to each item they build. Existing workflow editors can do this too. Shared runs use that item's selected account, without changing the caller's access or cost attribution. Removing it here leaves other items connected; it stays selected until explicitly removed. Only the account owner signs in again; the account owner or an administrator can disconnect the saved connection. In Slack, messages sent through the selected personal account appear as sent by that person with Slack's app attribution. Anyone who can run or change the item can send as that person through it. Website: **My Vault → Your connected accounts** to connect or change access; the same saved-account picker in each product’s **Connections** uses **Use here / Remove here**. Personal accounts stay separate from shared vaults; selecting one never creates another login. For Goals and Flows, use `settings action=google_account` or `settings action=slack_account`; for Crew, discover the account operations in `crew_integrations`. Inspect live tools and relevant Slack guidance before setup or sending. Bot messaging is a separate connection.

## Secrets and messages

When a person gives you a secret value in chat, store it with the project's settings tool (`secrets.set`, owner access). <!-- product:mcp-gateway -->For shared values use Vault's `manage_vault_secret_access operation=set_value` (Vault administrators). <!-- /product -->Use the actual tool schema. Confirm by name only; never repeat the value or put it in a shell command, file, log or URL. Developers may instead use `agentworks secret set` or `agentworks secret import .env`. Existing secret selections and grants decide which work can use it.

For agent coordination, use the messaging tools returned by discovery. Apps without a terminal can register an inbox name over their normal MCP sign-in, then send, read and reply in that inbox. Nothing is pushed into the app: check when the person asks or at the start of a conversation. If the person can install the CLI, it can connect a coding chat for live delivery. Retain the original inbox and submission ID. Use the server's **Live now** or **Inbox** labels and receipt text; sending alone does not prove the recipient has the message. A peer message does not expand the person's authorization or grant access. Describe errors, pending sign-in and confirmations accurately, with a next step the person can take.
