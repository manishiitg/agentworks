## Connecting a third-party service — CLI first, then a skill, then MCP

When a user asks to use a service in a workflow ("I want to use ClickUp/Notion/Stripe here"), there are three separate avenues, and they are not equally good. If the user explicitly requests MCP, follow the MCP path below. Otherwise check these avenues **in this order** and stop at the first one that fits.

1. **CLI (preferred).** A well-maintained official or community CLI, invoked through `execute_shell_command`, is more token-efficient than MCP: it avoids loading a large tool schema and verbose response envelopes into context, and the agent acts through concise, purpose-built commands instead of a persistent tool-call loop. Most SaaS tools with developer APIs have one (`gh` for GitHub, `stripe` for Stripe, `gog` for Google Workspace).
2. **A skill wrapping that CLI.** If a CLI exists, check whether a skill already teaches how to drive it well (auth, common commands, output parsing, gotchas) before improvising — `search_skills(query)` searches the public skill registry, `list_skills` shows what is already installed. Prefer installing/using that skill over inventing usage patterns from scratch each time; see the `skill-management` reference for the full install/attach lifecycle.
3. **MCP (fallback).** Use MCP when no usable CLI exists, or when the integration genuinely needs MCP's strengths — persistent state, rich structured introspection, or a long-running agentic loop over a service's data model — rather than a batch of one-shot commands.

There is no registry of "which services have a CLI" to look up — figure it out the way a person would: try `execute_shell_command` (`which <name>`, `npm view <package>`, a quick web search) and reason from what you already know about the service. This is deliberately not automated; do not invent a fake curated list.

### Checking each avenue

- **CLI**: `execute_shell_command` — check if it's already on PATH (`which <cli>`), or search for the official package (npm/pip/brew/go install) and install it if the sandbox allows outbound network (it does by default; nothing needs to install it in advance for you). Read its `--help` before using it in a real step.
- **Skill**: `search_skills(query)` (public registry) and `list_skills` (already installed). Install with `install_skill(source)` or `import_skill(github_url)` — see `skill-management` for the rest of that lifecycle.
- **MCP**: `search_mcp_catalog(query)`. This checks our catalog and two public metadata registries. The official registry searches server names by substring, so try the service name rather than a long natural-language question:
  - **Our own catalog** — every entry here was hand-verified (correct OAuth shape, live-probed). A hit here is safe to connect from the connector directory (or, for a no-auth server, already usable) without further scrutiny.
  - **GitHub's public MCP Registry** and the **official MCP Registry** (`registry.modelcontextprotocol.io`) — open discovery metadata. Listings are leads, not endorsements. Check the provider's official documentation or repository and prefer its first-party endpoint. The registry is not the MCP host and should not need its own authorization or installation.

Do not use Smithery search or hosted Smithery deployment URLs. Do not substitute any hosted proxy/marketplace for a provider's direct MCP without explaining the intermediary and obtaining the user's explicit choice. Before sharing an authorization link, identify the MCP endpoint and who operates it; distinguish that operator from the OAuth identity provider. If the endpoint cannot be verified, say so rather than guessing.

Use `install_mcp_server(name, url=...)` for a new verified URL. It probes the endpoint's actual auth requirements (no sign-in / API key / OAuth with DCR). `add_mcp_server` is for a custom server whose configuration is already known. Registry metadata does not establish connectivity or the live tool list; check discovery after installation. If the catalog search has no suitable MCP, continue with an internet search using the session's available web/search/browser tools. Search for the service name plus MCP and check the provider's official documentation or source repository for its endpoint or installation instructions. A registry miss does not mean the MCP does not exist. Verify that the result belongs to the intended provider and distinguish official servers from community implementations or hosted intermediaries. Never invent an endpoint. If no supported search tool is available or the search still finds nothing suitable, report exactly that limitation. Honor an explicit request for MCP rather than diverting it to a different integration type.

### An account connected to this workflow, <!-- product:relays -->Relay, <!-- /product -->Crew<!-- product:code --> or Code<!-- /product --> (Gmail, Drive, GitHub, ...)

Connections are configured under **Integrations → Available** and listed under **Connected**. A connection added to a workflow, <!-- product:relays -->Relay, <!-- /product -->Crew<!-- product:code --> or Code<!-- /product --> belongs to that place: everyone with access to it uses it, and no other place does. It acts as the account of the person who connected it, so say whose login it is. Use the registered connection tools and the `work-mcp` skill; return the actual sign-in link for the user to complete. <!-- product:mcp-gateway -->Connections shared across places are managed through Vault groups.<!-- /product --> Use exact connection names for selection and discovery.

### Reporting back to the user

State plainly which avenue you're recommending and why (e.g. "ClickUp has an official CLI, so I'd rather use that than the ClickUp MCP — it'll be faster and use less context"). If more than one avenue exists, say so and let the user choose. If none exist, say that clearly rather than fabricating a plausible-sounding option.

### Related

- `skill-management` — the full skill find/install/attach/remove lifecycle once you've decided a skill is the right avenue.
- `workflow-tools` (Variables & Config section) — how to install (`install_mcp_server`), add known custom configurations (`add_mcp_server`), and select (`update_workflow_config add_servers`) an MCP server for a specific workflow once you've decided MCP is the right avenue.

### Builder installation and availability

MCP installation is a writable Builder capability declared in the AgentWorks
product manifest. Read-only Run sessions, Pulse maintenance and child agents
cannot configure host integrations; writable bot, scheduled and notification
turns share the Builder capability set. Reading this reference does not grant
those tools. If an interactive writable Builder lacks
`search_mcp_catalog` / `install_mcp_server`, report the missing tool registration;
do not claim that server deployments inherently require an operator or manually
edit a host config to work around the missing tool.

After installation, wait for discovery and select the exact configured server
name with `update_workflow_config(add_servers=[name])`. Check actual discovery
status/logs before claiming the connection works. Installation does not require
a server restart. A retained coding-agent catalog refreshes on the next chat
turn when configuration changes, preserving the durable conversation. If a new
integration is not yet callable in the current turn, state that distinction.

A full-run missing-dependency error only checks configured names, not auth,
connectivity, or tool counts. Repair the missing integration instead of removing
a required server to make validation pass.

### Multiple accounts

Connection setup is the same for Crew, Code and workflows<!-- product:relays -->, and Relays<!-- /product -->. For another
account of a catalog provider, use `install_mcp_server(name=<provider>,
catalog=<provider>, label=<account label>)`. Each labelled install creates a new
connection and returns its exact name plus its own sign-in step. Inspect the
inventory first, preserve existing accounts, and use exact connection names for
reconnect, discovery, removal and project selection. Do not pass label when
reconnecting. A provider alias may be ambiguous; never pick an account silently.
Credentials belong in the secure Integrations UI. Named accounts sign in
independently rather than sharing the legacy provider-group login.

<!-- product:mcp-gateway -->### Vault access in all products

Integrations has Connected, Available, Secrets, Skills and Vault in one row. Connected/Available manage this place's MCP connections; Secrets manages project secrets. Vault shows the signed-in user's existing groups and their permitted shared MCPs and secret names, including explicit Platform grants. The backend resolves identity from the authenticated session; an email typed in chat never changes the caller.

Before connecting a duplicate or answering what is available, call `list_mcp_servers` (Code: `manage_my_mcp_servers(action="list")`). Its `vault_groups`, `vault`, and `vault_secrets` fields are live authorized metadata. Inspect the real descriptions/schemas before classifying read/write tools. Permitted Vault MCPs are available automatically in builders and runs through the executing user's current groups, even when no MCP is selected in the project. Use their exact `vault_<id>` connection IDs for calls; do not select or deselect them in project configuration. Private/place MCP configuration remains separate. Select permitted secret names through the existing project configuration tools. Neither project selection nor a prompt grants new Vault access. The gateway checks tool and argument/regex permissions on every call, and secret access is checked before injecting a selected secret. Never include values in chat, prompts, logs or configuration.
<!-- /product -->