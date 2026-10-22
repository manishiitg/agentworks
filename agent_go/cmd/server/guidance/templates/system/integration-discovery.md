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
  - **Our own catalog** — every entry here was hand-verified (correct OAuth shape, live-probed). A hit is a template to connect through chat using Vault; discovery verifies the live connection.
  - **GitHub's public MCP Registry** and the **official MCP Registry** (`registry.modelcontextprotocol.io`) — open discovery metadata. Listings are leads, not endorsements. Check the provider's official documentation or repository and prefer its first-party endpoint. The registry is not the MCP host and should not need its own authorization or installation.

Do not use Smithery search or hosted Smithery deployment URLs. Do not substitute any hosted proxy/marketplace for a provider's direct MCP without explaining the intermediary and obtaining the user's explicit choice. Before sharing an authorization link, identify the MCP endpoint and who operates it; distinguish that operator from the OAuth identity provider. If the endpoint cannot be verified, say so rather than guessing.

Use `install_mcp_server(name, url=...)` for a new verified URL. It probes the endpoint's actual auth requirements (no sign-in / API key / OAuth with DCR). `add_mcp_server` is for a custom server whose configuration is already known. Registry metadata does not establish connectivity or the live tool list; check discovery after installation. If the catalog search has no suitable MCP, continue with an internet search using the session's available web/search/browser tools. Search for the service name plus MCP and check the provider's official documentation or source repository for its endpoint or installation instructions. A registry miss does not mean the MCP does not exist. Verify that the result belongs to the intended provider and distinguish official servers from community implementations or hosted intermediaries. Never invent an endpoint. If no supported search tool is available or the search still finds nothing suitable, report exactly that limitation. Honor an explicit request for MCP rather than diverting it to a different integration type.

### Connect using Vault in this chat

Vault is the only MCP account store. Inspect list_mcp_servers, reuse exact saved
Vault IDs, and use install_mcp_server for catalog or verified custom endpoints.
Complete API-key and OAuth app setup here, including client_id/client_secret when
needed. Never repeat values. Return only the provider's real sign-in URL, verify
discovery after approval, then finish the requested project selection. A missing
app registration needs guidance at the provider using the returned callback URL;
never send the user to an old Connections menu or a secure AgentWorks form.
Projects keep existing selection and owner-lent use rules; Vault still checks
current grants, tool permissions and conditions. Do not widen access to connect.
Use the advertised UI tools only when the person asks to inspect a screen. If
unavailable, Ctrl+K (Cmd+K on Mac), search MCPs and select this project's result.
