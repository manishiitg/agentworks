# Private MCP servers in Code

Status: design, for review. Not built. Owner decision (2026-09-28): **a Code's
MCP servers are private to that Code only**. A Code never sees or uses the
shared platform MCP servers, and nothing outside the Code sees or uses its
servers. Parent design: [code_product.md](code_product.md).

## How MCP works today (the parts this changes)

- **One platform catalog.** The release's base config plus an overlay in the
  service state dir (`mcp_servers_<product>_user.json`,
  `agent_go/cmd/server/mcp_runtime_config.go`). `install_mcp_server` and
  `add_mcp_server` edit the overlay for the whole server.
- **Per-project selection.** A Crew's `workflow.json` capabilities hold
  `selected_servers` / `selected_tools`; chats only get selected servers.
- **Per-call authorization.** The bridge resolves each call through
  `resolveSelectedMCPServer` (`mcp_session_scope.go`): the server must be in
  the session's selection.
- **One login per server, for everyone.** OAuth tokens live at
  `<tokens root>/_platform/<server>.json` (`getUserTokenFilePath`,
  `oauth_routes.go`) and connections share the `mcp-platform` connection
  session. Anyone whose project selects the server acts as that one login.
- **Stdio servers run on the host**, outside any project sandbox.

Every one of these is platform-wide, so none can hold a private server.

## Decisions (recommended)

1. **Remote servers only.** HTTP (streamable) or SSE. No stdio, ever: a stdio
   server is an arbitrary program on the host, outside the Code's sandbox.
2. **The URL must be public.** Refuse loopback, private, link-local and
   cloud-metadata addresses, checked on every connect after DNS resolution
   (and on redirects), so a Code cannot use MCP to reach the server's own
   services or its network (SSRF).
3. **Owner only manages.** Only the Code's owner adds, connects (OAuth),
   reconnects and removes servers, like private Gmail. Co-owners and editors
   cannot change them.
4. **One login per Code.** The owner's OAuth login for a server belongs to
   that Code. Two Codes of the same owner that add the same server each
   connect separately.
5. **Everyone who can chat in the Code uses its servers** in their own chats,
   acting as the owner's login, and never sees tokens or credential headers.
   The UI tells the owner this when they share the Code. *(Alternative for
   the owner to decide: owner's chats only, as with Gmail. See Open
   questions.)*
6. **Fail closed.** A Code session resolves MCP only from its own registry.
   If the Code cannot be identified, no MCP server is available, never the
   platform catalog.

## Design

### Storage (outside the Code, server-owned)

A private store per Code, like private Gmail's gog store:

```
<AGENTWORKS_STATE_ROOT>/code-mcp/<sha256(owner, project)[:32]>/   (0700)
  servers.json      # the Code's servers: name, url, transport, oauth metadata
  tokens/<server>.json
  clients/<server>.json   # DCR client registrations
```

- Never inside the Code's folder: the agent, editors and the shell tool can
  read the Code's files, and a token there would leak. The Landlock read set
  is an allowlist that never includes this directory.
- Never reachable through the browser workspace proxy (it is not under the
  workspace docs root at all).
- `servers.json` holds no secrets. Credential headers (for servers that use
  an API key instead of OAuth) reference a **Code secret by name**, resolved
  at connect time, so the value stays in the Code's encrypted secrets.

### Server entry

```json
{ "name": "linear", "url": "https://mcp.linear.app/mcp", "transport": "http",
  "oauth": { "auth_url": "...", "token_url": "...", "registration_endpoint": "..." },
  "headers": { "Authorization": { "secret": "LINEAR_API_KEY", "format": "Bearer {}" } },
  "added_by": "<owner>", "added_at": "..." }
```

`name` is unique within the Code. The platform catalog (search) is only a
source of URLs and OAuth metadata to copy from; installing copies the entry
into the Code's `servers.json`, never a reference to the platform entry.

### Resolution and the bridge

- The Code session is already marked (`common.MarkCodeSession`, inherited by
  sub-agents). A new `resolveCodeMCPServer(sessionID, server, tool)` runs
  first in the bridge resolver: if `common.CodeSessionRoot(sessionID)` is set,
  it resolves **only** from that Code's `servers.json` plus the Code's
  `selected_servers` / `selected_tools`, sets `OAuth.TokenFile` to the Code's
  `tokens/<server>.json`, and uses the connection session
  `mcp-code-<hash>` so connections are never shared across Codes or with
  the platform. It never falls through to the platform resolver.
- Turn start: the tool list a Code chat registers comes from the same
  registry, not the platform catalog.
- Every connect re-checks the URL rules (decision 2).

### Agent tools and skill

- `mcp` feature on Code with an option `{servers: own}` (like
  `bots: {gmail: own}`). With it, the MCP tools act on the Code's registry:
  `list_mcp_servers`, `search_mcp_catalog` (read-only platform metadata),
  `install_mcp_server` (remote entries only; copies into the Code),
  `remove_mcp_server`, `trigger_mcp_discovery`, `get_mcp_server_logs`,
  `update_project_mcp_server_selection`.
- `add_mcp_server` accepts only a remote URL (no command/stdio).
- The management tools run only in the **owner's** chats (decision 3); in
  anyone else's chat they refuse with "only the owner manages this Code's MCP
  servers".
- The `code-mcp` skill (rendered from the shared template) gets a private-mode
  section: servers are this Code's own; remote only; the owner connects.

### UI

- Code Integrations shows an **Apps** tab again, backed by the Code's
  registry: the owner sees Add (search the catalog or paste a URL), Connect,
  Reconnect, Remove, and per-server tool selection. Others see the connected
  servers' names only.
- No shared-platform list, and no "platform connection" wording, in Code.

### Lifecycle

- **Remove a server:** delete its token and client files, close its
  connection, drop it from the selection.
- **Delete the Code:** delete the whole store directory and close every
  `mcp-code-<hash>` connection (alongside the existing share-list cleanup).
- **Unshare:** nothing to do; servers belong to the Code, not the person.
- **Owner account deleted/disabled:** tokens stay with the Code; its chats
  cannot connect once the owner is disabled (session start already refuses).

### Admin inspection, audit and cost

- Code inspection lists a Code's servers (name, URL, connected yes/no), never
  tokens or headers; the view is audited like the rest.
- MCP calls are already recorded in the cost ledger per session with the
  workspace path (`recordMCPBridgeCall`), so they land on the Code's cost row.

## Tests (end-to-end, not mocked)

1. Owner adds a public no-auth remote server (e.g. DeepWiki) to Code A; A's
   chat lists and calls its tool.
2. Code B (same owner) and a Crew cannot see or call it; the bridge refuses
   by name.
3. An editor's chat in A uses it (decision 5) but the editor cannot add,
   remove or reconnect, and never receives the token.
4. `add_mcp_server` with a stdio command, `http://127.0.0.1`, a private IP, a
   metadata IP, or a public name that resolves or redirects to one: refused.
5. OAuth server: owner connects; the token lands only in A's store; B's
   session cannot use it.
6. Delete A: store directory and connections are gone.
7. A Code session with no Code mark gets no MCP servers (fail closed).

## Open questions for the owner

1. **Who may use the Code's servers:** everyone who can chat in the Code
   (recommended, decision 5), or only the owner's chats (as Gmail)?
2. **API-key servers:** allow credential headers from Code secrets, or OAuth
   and no-auth servers only for now?
3. **Existing platform catalog:** keep it as a search source for installing
   privately (recommended), or hide it from Code entirely?
