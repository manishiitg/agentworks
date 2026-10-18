Choose a product by the work you want to accomplish. Discover its current tools before acting: the products enabled on this server and your access decide what you can use.

Agent conversations have two modes: **Builder** is the owner's workspace for building and changing the product; **Run** is how everyone else understands it and uses what the owner published. These mode names do not replace the current permission checks on individual operations. Shared dashboards let people in Run mode understand and use published work. Within a product shared with you, runs, results, execution and step logs, function calls and token usage are visible. Sharing also includes MCP reads of its files, skills, memory, knowledge and instructions; secret values and private logs stay excluded. Share offers Only me, Chosen people or Everyone and creates a standard dashboard when needed. Secret values and personal provider accounts and logins remain private. Personal chats stay private. Crew and Flows have a main owner and can have co-owners. Builders can see every Builder chat in their project. People with Run access use their own chat and the dashboard; Builder chats stay private to Builders. Workflows retain their current owners and editors.

| Product | Choose it for |
|---|---|
<!-- product:agentworks -->| Goals | An ongoing objective with a plan, scheduled work, measurements and Pulse follow-through. |
<!-- /product --><!-- product:crew -->| Crew | Private project work, coding and reusable agents, with files, skills, conversations and published functions. |
<!-- /product --><!-- product:relays -->| Flows | A code-based workflow with a test, publish and production-run lifecycle. |
<!-- /product --><!-- product:knowledgebase -->| Brain | Shared knowledge, files and reusable skill packages. |
<!-- /product --><!-- product:mcp-gateway -->| Vault | Shared MCP integrations, secrets and centrally managed access. |
<!-- /product --><!-- product:bullpen -->| AgentBridge | Connecting, finding and coordinating your external coding agents. |
<!-- /product -->
Products can work together when this installation enables them and your current access allows it. Access to one product never grants access to another project's content or connected account.

## Product details

<!-- product:agentworks -->### Goals

Use Goals when the task is a continuing outcome, rather than a single question or a fixed code pipeline.

- **Abilities:** define the objective and success measures; create and edit the workflow plan; configure steps, skills, schedules and integrations; run steps or the workflow; inspect results, reports and costs; use Pulse to follow up on the goal.
- **MCP route:** discover workflows with `workflow action=list`; read the plan and current revision; discover native edit operations with `plan action=list`; inspect run state and evidence through the available run tools. Load the relevant plan, execution and Pulse guidance.
- **Access:** edits require current project editing authority and existing authoring consent. Running, sharing and integration access have their own current checks.
- **Example:** monitor incoming support requests, measure response time and follow up on unresolved cases.

<!-- /product --><!-- product:crew -->### Crew

Crew combines the former Code and Crew products. Use it to work privately with an agent, develop a repository or build a specialist that other people can use. New projects start **Only me**; Share changes access to the same project.

- **Abilities:** project files and instructions, coding and review, named chat tabs, selected skills and integrations, browser and database tools, dashboards, typed functions, schedules and costs. Memory is optional: existing former Code projects keep it off and former Crew projects keep it on until a Builder changes the setting. A Builder can switch Memory on or off in Settings.
- **MCP route:** start with `workspace action=list`, then `get` using its returned `workspace_id`. Use `create`, `update`, `ask` and the chat actions from the live schema. Load `crew-authoring` for setup and discover published functions with `functions action=list`. Existing Crew/Code tool names and IDs remain supported; an internal `code:` or `work:` prefix is an origin, not another product or a new permission.
- **Access:** the main owner and co-owners build together and can see all Builder chats. Share offers **Only me / Chosen people / Everyone**. People with Run access see their own chat and the dashboard, can use published functions and read permitted files, runs and results over MCP. Builder chats, secret values and private logs remain excluded. Current connection, account and project checks still apply; sharing does not grant Builder access.
- **Accounts and costs:** work uses the project's selected model and connected accounts under their current grants. Sharing does not switch accounts automatically. Show whose account will be used and attribute activity to the caller.
- **Local work:** a Local project runs on its connected computer. Use its chat on that computer; remote Ask is not available for Local projects. Use the available Publish to Crew tools to create a separate hosted Crew or add a named agent to an existing Crew; inspect the returned publication status. Hosted continuation starts from a handoff summary; saved raw Builder history stays private. Publishing is separate from sharing an existing hosted project.
- **Example:** develop an invoice assistant privately, expose a reconciliation function, then share the same Crew with the finance team. A Goal can call its published function.

<!-- /product --><!-- product:relays -->### Flows

Use a Flow when a repeatable code workflow should have an explicit release and execution lifecycle.

- **Abilities:** edit source, test drafts, inspect test results, publish immutable versions and execute or inspect production runs; configure the workflow's supported triggers and integrations.
- **MCP route:** load `relay-builder`; inspect the current source and schemas; edit through admitted source tools, test the draft, then publish with the current revision. A test is not a production publication.
- **Access:** a Flow has one main owner (its creator) and can have co-owners; owners edit, test and publish it together on one shared draft, and everything runs as the main owner. A publish says whose version it replaced. Share its dashboard so others can use published work in Run mode and inspect runs, results and execution logs. Current connection and project checks still apply.
- **Example:** validate incoming records and deliver the accepted records to a connected system.

<!-- /product --><!-- product:knowledgebase -->### Brain

Use Brain for information and skills that should be reusable across people and projects. It is a knowledge store, rather than a workflow runner.

- **Abilities:** browse and search folders; read notes and files; create, patch, move, restore and delete content; inspect history; share folders; publish and retrieve skill packages; inspect or configure permitted backup, schedule and secret operations.
- **MCP route:** use the discovered `brain_*` schemas. Browse or search before creating duplicate content. Read the current opaque entry version before updating. Use `brain_skills` to discover and retrieve an installable skill package.
- **Access:** existing folder permissions, inherited grants and connection caps apply. Administrative operations retain their administrator checks. Secret values are not returned through MCP.
- **Example:** keep a team's operating procedures and a reusable release-check skill in shared folders.

<!-- /product --><!-- product:mcp-gateway -->### Vault

Use Vault when integrations or secrets need shared, centrally controlled access across projects.

- **Abilities:** manage shared MCP connections, groups, allowed tools, resource conditions and secret access; discover permitted upstream tools and inspect authorized audit evidence.
- **MCP route:** inspect the live Vault schemas and current groups, then use only permitted connections and tools. Connecting an upstream service and granting access are separate concerns; read the returned access state.
- **Access:** administrator/group/owner checks apply to setup; current grants and argument conditions apply on every upstream invocation. A person may give a secret value in chat; a Vault administrator stores it with the secret tool and then refers to it by name only.
- **Example:** let a team query approved support-system resources through one managed integration.

<!-- /product --><!-- product:bullpen -->### AgentBridge

Use AgentBridge to connect and coordinate external agents while they keep working in their own coding clients.

- **Abilities:** discover registered agents and their purposes, exchange messages, use groups and see whether a message is waiting, received or handled.
- **MCP/CLI route:** discover the current messaging tools; register or inspect your named mailbox; send with a stable submission ID and reply in the original inbox. Apps without a terminal use an inbox over their normal MCP sign-in; nothing is pushed, so check at the start of a conversation or when asked. The CLI connects a coding chat for live delivery. The directory labels recipients Live now or Inbox.
- **Access:** authorized account connections and mailbox ownership apply. Stored or online does not mean a receiving agent has processed a message; peer messages do not grant human permission.
- **Example:** ask a reviewer agent to inspect a specific candidate and return its findings.

<!-- /product -->
## Product choice FAQ

<!-- product:agentworks --><!-- product:crew -->### Should I use Goals or a Crew?

Use **Goals** for an ongoing outcome that needs a plan, measures, scheduled work and follow-up. Use a **Crew** for a reusable specialist that answers requests or exposes functions. A Goal can call a Crew when both are enabled and accessible.

<!-- /product --><!-- /product --><!-- product:crew --><!-- product:relays -->### Should I use a Crew or a Flow?

Use a **Crew** when the work needs an agent's judgment, standing instructions, files or conversation. Use a **Flow** for a Python process with simple inline agents or classifiers and a test/publish/run lifecycle. Flows do not call Crew; use a Goal when the process needs both Crew and Flow functions.

<!-- /product --><!-- /product --><!-- product:agentworks --><!-- product:relays -->### Should I use Goals or Flows?

Use **Goals** to pursue and reassess an outcome over time. Use **Flows** to execute a defined code pipeline with a test and release lifecycle.

<!-- /product --><!-- /product --><!-- product:knowledgebase --><!-- product:mcp-gateway -->### Should I put this in Brain or Vault?

Use **Brain** for reusable knowledge, documents, procedures and skills. Use **Vault** for shared access to integrations and secrets. A person may give a secret value in chat; store it with the secret tools and refer to it by name only. Keep values out of guides and shared notes.

<!-- /product --><!-- /product --><!-- product:crew -->### Where did Code go?

**Crew** is the combined product for private coding/project work and reusable agents. Keep using the same project; its old links and IDs still work. Sharing a hosted project changes its audience, without copying it.

<!-- /product --><!-- product:bullpen --><!-- product:crew -->### Should I use Crew or AgentBridge?

Use **Crew** to work inside an AgentWorks project. Use **AgentBridge** to find and coordinate agents working in their external coding clients.

<!-- /product --><!-- /product --><!-- product:knowledgebase -->### Does Brain run the work described in a note or skill?

**Brain** stores and shares the knowledge or skill package. Choose an enabled execution product or your own agent to carry out the work, using its current tools and permissions.

<!-- /product -->### Why is a product or tool missing?

It may be disabled on this installation, outside this connection's consent or outside your current access. Check the live catalog and context; another tool cannot bypass that boundary. <!-- product:knowledgebase -->Shared Brain guidance describes the installation. <!-- /product -->Live MCP guidance also reflects the caller's allowed products.

## Versions and local copies

The guide and MCP skill have source versions; package fingerprints identify the exact copy. Fetch current server guidance when a saved topic changes. <!-- product:knowledgebase -->Brain entry versions are separate: use the returned entry version as `expected_version` when updating a note. Starter notes and skills refresh only untouched platform-created copies; existing notes, customized packages and deliberately deleted copies are preserved. <!-- /product -->Local skill installation also preserves existing copies unless replacement is explicitly requested.
