Choose a product by the work you want to accomplish. Discover its current tools before acting: the products enabled on this server and your access decide what you can use.

Agent conversations have two modes: **Builder** is the owner's workspace for building and changing the product; **Run** is how everyone else understands it and uses what the owner published. These mode names do not replace the current permission checks on individual operations. Shared dashboards let people in Run mode understand and use published work. Within a product shared with you, runs, results, execution and step logs, function calls and token usage are visible. Secret values and personal provider accounts and logins remain private. Personal chats stay private. Code, Crew and Relays have one owner; other people use published work through the product's shared dashboard. Workflows retain their current owners and editors.

| Product | Choose it for |
|---|---|
<!-- product:agentworks -->| Goals | An ongoing objective with a plan, scheduled work, measurements and Pulse follow-through. |
<!-- /product --><!-- product:work -->| Crew | A reusable agent with standing instructions, skills and exposed functions. |
<!-- /product --><!-- product:relays -->| Relays | A code-based workflow with a test, publish and production-run lifecycle. |
<!-- /product --><!-- product:knowledgebase -->| Brain | Shared knowledge, files and reusable skill packages. |
<!-- /product --><!-- product:mcp-gateway -->| Vault | Shared MCP integrations, secrets and centrally managed access. |
<!-- /product --><!-- product:code -->| Code | Repository work and private coding-agent projects. |
<!-- /product --><!-- product:bullpen -->| Bullpen | Connecting, finding and coordinating your external coding agents. |
<!-- /product -->
Products can work together when this installation enables them and your current access allows it. Access to one product never grants access to another project's content or connected account.

## Product details

<!-- product:agentworks -->### Goals

Use Goals when the task is a continuing outcome, rather than a single question or a fixed code pipeline.

- **Abilities:** define the objective and success measures; create and edit the workflow plan; configure steps, skills, schedules and integrations; run steps or the workflow; inspect results, reports and costs; use Pulse to follow up on the goal.
- **MCP route:** discover workflows with `workflow action=list`; read the plan and current revision; discover native edit operations with `plan action=list`; inspect run state and evidence through the available run tools. Load the relevant plan, execution and Pulse guidance.
- **Access:** edits require current project editing authority and existing authoring consent. Running, sharing and integration access have their own current checks.
- **Example:** monitor incoming support requests, measure response time and follow up on unresolved cases.

<!-- /product --><!-- product:work -->### Crew

Use a Crew for a specialist you want to call repeatedly from your agent, another project or an enabled messaging channel.

- **Abilities:** persistent purpose and instructions, project files, memory, selected skills and integrations; admitted shell, browser and database tools; typed functions with input/result schemas; ask and message entry points; schedules and caller-specific conversations.
- **MCP route:** discover with `crew action=list`, inspect the selected Crew, load `crew-authoring`, and discover exposed functions with `functions action=list`. Call functions or ask only through the entry points currently admitted for that Crew and caller.
- **Access:** a Crew has one owner, who changes its setup. Share its dashboard to let others use it in Run mode through Ask and published functions. Functions run with the owner's authority and may change the Crew; callers do not gain setup access. Current account, connection and project checks still apply.
- **Example:** a document specialist exposes a function that accepts a file and returns a structured summary.

<!-- /product --><!-- product:relays -->### Relays

Use a Relay when a repeatable code workflow should have an explicit release and execution lifecycle.

- **Abilities:** edit source, test drafts, inspect test results, publish immutable versions and execute or inspect production runs; configure the workflow's supported triggers and integrations.
- **MCP route:** load `relay-builder`; inspect the current source and schemas; edit through admitted source tools, test the draft, then publish with the current revision. A test is not a production publication.
- **Access:** a Relay has one owner, who edits, tests and publishes it. Share its dashboard so others can use published work in Run mode and inspect runs, results and execution logs. Current connection and project checks still apply.
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

<!-- /product --><!-- product:code -->### Code

Use Code for repository development: implementing changes, investigating failures and reviewing code in a coding-agent workspace.

- **Abilities:** private code projects and chats, repository/file work, coding-agent execution, permitted integrations and review/audit views.
- **MCP route:** use only the Code operations actually returned by this connection. A product's full app/CLI ability is not a promise that every action exists as a direct MCP tool.
- **Access:** a Code project belongs to one person. Others use what it publishes through its shared dashboard and the entry points the current tools expose. Repository and chat work remain with the owner; current review/account checks still apply.
- **Example:** implement a bounded bug fix and inspect its changes and verification results.

<!-- /product --><!-- product:bullpen -->### Bullpen

Use Bullpen to connect and coordinate external agents while they keep working in their own coding clients.

- **Abilities:** discover registered agents and their purposes, exchange messages, use groups and track delivery/admission/processing receipts.
- **MCP/CLI route:** discover the current messaging tools; register or inspect your named mailbox; send with a stable submission ID and reply in the original inbox. The person can request attachment of their own detected coding chat through the CLI.
- **Access:** authorized account connections and mailbox ownership apply. Stored or online does not mean a receiving agent has processed a message; peer messages do not grant human permission.
- **Example:** ask a reviewer agent to inspect a specific candidate and return its findings.

<!-- /product -->
## Product choice FAQ

<!-- product:agentworks --><!-- product:work -->### Should I use Goals or a Crew?

Use **Goals** for an ongoing outcome that needs a plan, measures, scheduled work and follow-up. Use a **Crew** for a reusable specialist that answers requests or exposes functions. A Goal can call a Crew when both are enabled and accessible.

<!-- /product --><!-- /product --><!-- product:work --><!-- product:relays -->### Should I use a Crew or a Relay?

Use a **Crew** when the work needs an agent's judgment, standing instructions, memory or conversation. Use a **Relay** when the work is a repeatable code pipeline that you want to test and publish as a version before production runs.

<!-- /product --><!-- /product --><!-- product:agentworks --><!-- product:relays -->### Should I use Goals or Relays?

Use **Goals** to pursue and reassess an outcome over time. Use **Relays** to execute a defined code pipeline with a test and release lifecycle.

<!-- /product --><!-- /product --><!-- product:knowledgebase --><!-- product:mcp-gateway -->### Should I put this in Brain or Vault?

Use **Brain** for reusable knowledge, documents, procedures and skills. Use **Vault** for shared access to integrations and secrets. A person may give a secret value in chat; store it with the secret tools and refer to it by name only. Keep values out of guides and shared notes.

<!-- /product --><!-- /product --><!-- product:code --><!-- product:work -->### Should I use Code or a Crew?

Use **Code** to develop a repository, investigate failures or review code changes. Use a **Crew** for a reusable specialist that serves requests across projects.

<!-- /product --><!-- /product --><!-- product:code --><!-- product:bullpen -->### Should I use Code or Bullpen?

Use **Code** for repository work in a coding-agent workspace. Use **Bullpen** to find and coordinate agents already working in their external coding clients.

<!-- /product --><!-- /product --><!-- product:knowledgebase -->### Does Brain run the work described in a note or skill?

**Brain** stores and shares the knowledge or skill package. Choose an enabled execution product or your own agent to carry out the work, using its current tools and permissions.

<!-- /product -->### Why is a product or tool missing?

It may be disabled on this installation, outside this connection's consent or outside your current access. Check the live catalog and context; another tool cannot bypass that boundary. <!-- product:knowledgebase -->Shared Brain guidance describes the installation. <!-- /product -->Live MCP guidance also reflects the caller's allowed products.

## Versions and local copies

The guide and MCP skill have source versions; package fingerprints identify the exact copy. Fetch current server guidance when a saved topic changes. <!-- product:knowledgebase -->Brain entry versions are separate: use the returned entry version as `expected_version` when updating a note. Starter notes and skills refresh only untouched platform-created copies; existing notes, customized packages and deliberately deleted copies are preserved. <!-- /product -->Local skill installation also preserves existing copies unless replacement is explicitly requested.
