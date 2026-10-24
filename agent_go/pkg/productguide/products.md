AgentWorks helps a person describe a business process, build a working version with their AI app, and share it with their team. Start with the work: the information people provide, the result they need and who will use it. The AI chooses the appropriate enabled product; people do not need to learn the catalog before starting. Respect a product they explicitly choose.

A marketing lead might ask for campaign reviews, a support lead for policy-based reply drafts, or an operations lead for a recurring follow-up process. Reuse the team's available knowledge and connections, try a representative example, then return a usable team link with a clear explanation of what works and any missing setup. ChatGPT and Cowork use the remote connection; teammates can use the shared website without installing MCP or the same AI app.

### Build, share and contribute

- **Build:** the website Builder and a person's AI app over MCP use the same current access. Builders create and improve the process; a shared project's Run experience is how teammates use it.
- **Share:** choose **Only me / Chosen people / Everyone**. Sharing creates a standard dashboard when needed, with Ask and published actions; it does not make teammates Builders.
- **Contribute:** teammates bring inputs, case context and suggestions through the shared entry points. Editing shared knowledge follows its existing folder access. When requested, use existing co-owner/editor access for people who will help build; explain that Builders can see Builder chats.

Within shared work, current product access covers its runs, results, execution logs and token usage, plus permitted MCP file reads. Secret values, private logs and personal provider logins stay protected. Shared runs use the product's selected accounts under their existing grants. Use current tool evidence to distinguish a created project from a completed example or a working shared link. A five-minute first version is an aim to verify, not a guarantee for every process or missing connection.

Supporting AI apps can follow long function calls and Builder work as MCP Tasks, and show an elicitation form when work needs an ordinary answer. Crew Ask can also pause for questions in supporting apps; answer in the same chat, and the Crew continues with your existing access. Account sign-in opens a browser flow. These use the same project and account permissions; client support varies, so discover current capabilities and inspect actual completion.

| Product | Choose it for |
|---|---|
<!-- product:agentworks -->| Goals | Ongoing outcomes that need a plan, measurements, scheduled work and follow-up. |
<!-- /product --><!-- product:crew -->| Crew | A reusable specialist or shared workspace for requests, knowledge, files and team actions; also coding work. |
<!-- /product --><!-- product:relays -->| Flows | Repeatable code pipelines with a test, publish and production-run lifecycle. |
<!-- /product --><!-- product:knowledgebase -->| Brain | Shared knowledge, files and reusable skill packages. |
<!-- /product --><!-- product:mcp-gateway -->| Vault | Shared MCP integrations, secrets and centrally managed access. |
<!-- /product --><!-- product:bullpen -->| AgentBridge | Connecting, finding and coordinating your external coding agents. |
<!-- /product -->
Products can work together when this installation enables them and current access allows it. Discover live tools before acting. A person's MCP sign-in follows their website access; machine, service-account and device connections retain their assigned limits. Access to one product never grants access to another project's content or connected account.

## Product details

<!-- product:agentworks -->### Goals

Use Goals when the task is a continuing outcome, rather than a single question or a fixed code pipeline.

- **Abilities:** define the objective and success measures; create and edit the workflow plan; configure steps, skills, schedules and integrations; run steps or the workflow; inspect results, reports and costs; use Autopilot to follow up on the goal.
- **MCP route:** discover workflows with `workflow action=list`; read the plan and current revision; discover native edit operations with `plan action=list`; inspect run state and evidence through the available run tools. Load the relevant plan, execution and Autopilot guidance.
- **Access:** owners and workflow editors can build through MCP just as on the website. Running, sharing and integration access use the same server checks.
- **Example:** monitor incoming support requests, measure response time and follow up on unresolved cases.

<!-- /product --><!-- product:crew -->### Crew

Crew combines the former Code and Crew products. Use it to work privately with an agent, develop a repository or build a specialist that other people can use. New projects start **Only me**; Share changes access to the same project.

- **Abilities:** project files and instructions, coding and review, named chat tabs, selected skills and integrations, browser and database tools, dashboards, typed functions, schedules and costs. Memory is optional: existing former Code projects keep it off and former Crew projects keep it on until a Builder changes the setting. A Builder can switch Memory on or off in Settings.
- **MCP route:** start with `workspace action=list`, then `get` using its returned `workspace_id`. Use `create`, `update`, `ask` and the chat actions from the live schema. Load `crew-authoring` for setup and discover published functions with `functions action=list`. Keep the canonical `workspace_id` returned by discovery; do not construct retired product selectors.
- **Access:** the main owner and co-owners build together and can see all Builder chats. Share offers **Only me / Chosen people / Everyone**. People with Run access see their own chat and the dashboard, can use published functions and read permitted files, runs and results over MCP. Builder chats, secret values and private logs remain excluded. Current connection, account and project checks still apply; sharing does not grant Builder access.
- **Accounts and costs:** work uses the project's selected model and connected accounts under their current grants. Sharing does not switch accounts automatically. Show whose account will be used and attribute activity to the caller.
- **Optional laptop access:** Crew runs on the server, including its files, shell, functions, schedules and background work. Connect your computer once in My Vault, then choose it and its folders in the Goal or Crew Local laptop panel. Each folder is Read (list and copy files into Crew) or Read & write (also write files); running terminal commands is a separate switch, "Run terminal commands", that also needs a Read & write folder. Use `manage_laptop_access` for settings and folder grants, `list_local_files`, `write_local_file`, `copy_local_file_to_project` for files and `execute_local_shell_command` for commands, using their live schemas. Laptop access belongs to the owner’s interactive Builder chat; Run callers, schedules and Autopilot do not inherit it. A disconnected laptop leaves normal server work available. There is no separate Publish copy, sync or copy back to the laptop; server secrets are not sent to laptop commands.
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

- **Abilities:** discover registered agents and their purposes, exchange messages and see whether a message is waiting, received or handled.
- **MCP/CLI route:** discover the current messaging tools; register or inspect your named mailbox; send with a stable submission ID and reply in the original inbox. Apps without a terminal use an inbox over their normal MCP sign-in; nothing is pushed, so check at the start of a conversation or when asked. The CLI connects a coding chat for live delivery. The directory labels recipients Live now or Inbox.
- **Access:** authorized account connections and mailbox ownership apply. Stored or online does not mean a receiving agent has processed a message; peer messages do not grant human permission.
- **Example:** ask a reviewer agent to inspect a specific candidate and return its findings.

<!-- /product -->
## Product choice FAQ

### I know my business process. Must I choose a product first?

No. Describe what the team does, the information it uses, the result it needs and who should use it. Your connected AI chooses an appropriate product, asks only for essential missing details and builds a small working version. A useful handoff includes a checked example, a working link, the saved audience and how teammates contribute. Missing account sign-in or setup must be named clearly.

### Does sharing let everyone change how it works?

Sharing gives people the current Run experience: Ask, published actions and their dashboard. They can supply work, add context and suggest improvements through those entry points. Shared knowledge edits need its existing folder access. Ask to add a co-owner or workflow editor when someone should help build; that access includes the project's Builder conversations. Use existing roles, not a new contribution permission.


<!-- product:agentworks --><!-- product:crew -->### Should I use Goals or a Crew?

Use **Goals** for an ongoing outcome that needs a plan, measures, scheduled work and follow-up. Use a **Crew** for a reusable specialist that answers requests or exposes functions. A Goal can call a Crew when both are enabled and accessible.

<!-- /product --><!-- /product --><!-- product:crew --><!-- product:relays -->### Should I use a Crew or a Flow?

Use a **Crew** when the work needs an agent's judgment, standing instructions, files or conversation. Use a **Flow** for a Python process with simple inline agents or classifiers and a test/publish/run lifecycle. Flows do not call Crew; use a Goal when the process needs both Crew and Flow functions.

<!-- /product --><!-- /product --><!-- product:agentworks --><!-- product:relays -->### Should I use Goals or Flows?

Use **Goals** to pursue and reassess an outcome over time. Use **Flows** to execute a defined code pipeline with a test and release lifecycle.

<!-- /product --><!-- /product --><!-- product:knowledgebase --><!-- product:mcp-gateway -->### Should I put this in Brain or Vault?

Use **Brain** for reusable knowledge, documents, procedures and skills. Use **Vault** for shared access to integrations and secrets. A person may give a secret value in chat; store it with the secret tools and refer to it by name only. Keep values out of guides and shared notes.

<!-- /product --><!-- /product --><!-- product:crew -->### Where did Code go?

**Crew** is the combined product for private coding/project work and reusable agents. Find the current Crew with `workspace action=list` and retain its returned identity. Sharing a hosted project changes its audience, without copying it.

<!-- /product --><!-- product:bullpen --><!-- product:crew -->### Should I use Crew or AgentBridge?

Use **Crew** to work inside an AgentWorks project. Use **AgentBridge** to find and coordinate agents working in their external coding clients.

<!-- /product --><!-- /product --><!-- product:knowledgebase -->### Does Brain run the work described in a note or skill?

**Brain** stores and shares the knowledge or skill package. Choose an enabled execution product or your own agent to carry out the work, using its current tools and permissions.

<!-- /product -->### Why is a product or tool missing?

It may be disabled on this installation or outside your current website access. A machine or device connection may also have narrower limits. Check the live catalog and context; another tool cannot bypass that boundary. <!-- product:knowledgebase -->Shared Brain guidance describes the installation. <!-- /product -->Live MCP guidance also reflects the caller's allowed products.

## Versions and local copies

The guide and MCP skill have source versions; package fingerprints identify the exact copy. Fetch current server guidance when a saved topic changes. <!-- product:knowledgebase -->Brain entry versions are separate: use the returned entry version as `expected_version` when updating a note. Starter notes and skills refresh only untouched platform-created copies; existing notes, customized packages and deliberately deleted copies are preserved. <!-- /product -->Local skill installation also preserves existing copies unless replacement is explicitly requested.

<!-- product:work -->## Crew email inboxes

<!-- product:work -->Crew inboxes are available on servers. Connect Gmail in My Vault, grant Gmail read access, and mark one account **Use for Crew inboxes**. Incoming email is on by default for each Crew once its owner selects the account; an explicit off choice stays off. Open **Inbox** in the Crew’s right panel to copy its address. Ask Builder to turn receiving off or back on for that Crew. Only enabled Crew addresses are checked, about every 60 seconds. Copy the address and read received conversations and attachments there. Ask Builder to disable receiving when needed; stored mail stays available. Only mail addressed to that Crew appears there, with existing Crew sharing. Other personal email stays private. No company receiving mailbox or Google Cloud receiving setup is needed. Disconnecting the selected account stops delivery to all your Crews. Receiving stores mail; optional Watchers choose what work to start. Local deployments do not have Crew inboxes.


<!-- product:work -->### Watch your existing Gmail

<!-- product:work -->Ask your Crew Builder to watch your personal Gmail and send only matching content to your Crew chat. It uses the same Watcher setup: check_mode=inbox, email_source=personal_gmail, with Python supplied through script_content. You must own the Crew and select your own Gmail inbox account in My Vault with read access. The server's email feature must be enabled. A dedicated Crew email address is optional for this use.

<!-- product:work -->It watches new received mail, including archived arrivals, about every60seconds; it excludes sent mail and drafts and does not import old mail. Private message bodies exist only in the temporary detector snapshot, up to20 at a time, then are deleted. Message identities and the Watcher cursor stay private; personal mail is never listed in the Crew Inbox. Only the event payload chosen by your Watcher is sent to your chat or published function. Use target kind=agent and a message for chat; functions must be declared entry points. Changing the selected Gmail requires a new Watcher.
