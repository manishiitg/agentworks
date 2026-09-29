# MCP servers in Code: global and personal

Status: built (2026-09-28), implementation reviewed by ai-work-7e; design reviewed twice. Folded in: fail-closed from durable facts, report-run resolver (global only), unique internal names, tokens encrypted at rest, SSRF client, no shell exposure of MCP keys, personal headers from personal secrets only, pinned session person, protected workflow.json, enablement in the personal store. Not built. Parent design:
[code_product.md](code_product.md).

Owner decisions (2026-09-28):

1. **Everyone who chats in a Code uses MCP, each with their own servers.** A
   person's own MCP servers and logins are theirs alone: nobody else in the
   Code (not the owner, not an editor) uses or sees them.
2. **Secrets for MCP are personal too.** A server that needs an API key takes
   it from the chatting person's own secrets, never from someone else's.
3. **Global MCP servers and global secrets also work in Code**, as they do in
   a Crew: the admin-provided platform servers and global secrets can be
   selected.

So a Code chat sees two kinds of server: **global** (platform, admin-managed,
shared login, as today) and **personal** (the chatting person's own).

## How MCP works today (the parts this changes)

- **One platform catalog.** The release's base config plus an overlay in the
  service state dir (`mcp_servers_<product>_user.json`,
  `agent_go/cmd/server/mcp_runtime_config.go`). `install_mcp_server` and
  `add_mcp_server` edit it for the whole server.
- **Per-project selection.** A project's `workflow.json` capabilities hold
  `selected_servers` / `selected_tools`.
- **Per-call authorization.** The bridge resolves each call through
  `resolveSelectedMCPServer` (`mcp_session_scope.go`): the server must be in
  the session's selection.
- **One login per global server, for everyone.** OAuth tokens at
  `<tokens root>/_platform/<server>.json` (`getUserTokenFilePath`,
  `oauth_routes.go`); connections share the `mcp-platform` session.
- **Secrets.** Global secrets (admin-managed, selected per project) and
  project secrets, which since PLAT-272 are shared by everyone with access to
  the project (`workflow_shared_secrets.go`). There is no personal secret
  store today.
- **Stdio servers run on the host**, outside any project sandbox.

## Model

| | Global servers | Personal servers |
|---|---|---|
| Who adds them | Admins (platform catalog, as today) | Any person, for themselves |
| Login | The one platform login (`_platform`) | That person's own login |
| Which run in a Code chat | The Code's selection, set by owner/co-owners | The chatting person's own, switched on per Code by them |
| Who sees credentials | Nobody in Code (admin only) | Nobody but the store; not even the person after connecting |
| Transport | As the platform allows | **Remote only** (HTTP/SSE), never stdio |

Secrets in the same shape: **global secrets** (selected for the Code as in a
Crew) and **personal secrets** (the chatting person's own). A Code chat never
resolves another person's personal secret. **A personal server's credential
headers may reference only that person's personal secrets**, never a global or
project secret: otherwise anyone could add a personal server at their own URL
with a header naming a global secret the Code selected, and the platform would
send them its value, turning "usable, never seen" into readable.

## Decisions (recommended)

1. **Personal servers are per person, reusable across their Codes.** Connect
   once; switch each on per Code. Never usable in anyone else's chat, and
   never in Crews or workflows (Code only, for now).
2. **Remote only, public URLs only** for personal servers. No stdio. Refuse
   loopback, private, link-local and cloud-metadata addresses, checked on every
   connect after DNS resolution and on every redirect, including OAuth
   endpoints, so MCP cannot reach the server's own services or network (SSRF).
3. **The chat's person decides.** A chat's MCP set is: the Code's global
   selection + the chatting person's personal servers enabled for this Code.
   Background work and sub-agents inherit the person of the chat that started
   them. Slack/WhatsApp DMs run as the person who sent them.
4. **Fail closed, from durable facts.** Whether a session is a Code session,
   and whose, is decided from the session's durable record (owner + a
   workspace path under `Chats/Code/projects`, from the event store / session
   record), not from the in-memory `common.CodeSessionRoot` mark, which a
   restart loses while a retained CLI keeps calling the bridge with its old
   session id. For a Code session the resolver returns an error, never
   `(nil, nil)`: today `(nil, nil)` lets mcpagent's executor fall through to
   the session registry, the codeexec global registry and finally `mcpcache`,
   which dials any server name from the global config. If the person or Code
   cannot be established: no personal servers, no personal secrets, and the
   global selection only after the Code is established.
5. **Admin inspection** shows which personal servers a person enabled in a
   Code (name, URL with its query string stripped, connected), never tokens or
   secret values; audited.
6. **Nobody acts as someone else's login.** Only the chatting person's own
   personal servers run in their chat; there is no "use the owner's login"
   path. (The first draft's "editors act as the owner" is gone.)

## Design

### Personal store (server-owned, outside every workspace)

```
<AGENTWORKS_STATE_ROOT>/personal-mcp/<sha256(user)[:32]>/   (0700)
  servers.json        # name, url, transport, oauth metadata, header refs
  tokens/<server>.json.enc    # AES-GCM, AAD = this store's id
  clients/<server>.json.enc   # dynamic client registrations, same sealing
```

- **Encrypted at rest.** Tokens and client registrations are sealed with the
  server secrets key (AES-GCM, AAD = the store id, like `workflow_secrets`)
  and loaded through a `TokenStore` implementation handed to mcp-go, never a
  plaintext `TokenFile`. Reason: coding CLIs in *hybrid* mode (other users'
  Crews and workflows) use their native Read tool on anything the service
  user can read, including `state/`. The same exposure exists today for
  `_platform` tokens and the gog stores (tracked as PLAT-364 part 2); this
  design does not add a new plaintext copy.
- **Never grant `state/`** in any sandbox policy (cli-runtimes also live
  there); Landlock's read set is an allowlist and today excludes it.

- Never inside a Code or the user's workspace tree: the agent and anyone with
  file access to a Code can read its files. The Landlock read set is an
  allowlist that never includes this directory; the browser workspace proxy
  cannot reach it (not under the docs root).
- Per-Code enablement lives **in the person's own store**
  (`enabled.json`: `{ "<code root>": ["linear", ...] }`), not in the Code's
  `workflow.json`: editors can write `workflow.json`, and nobody but the
  person may switch their servers on or off. Access to the Code is re-checked
  at resolution, so a stale entry for a Code the person lost grants nothing.

### Outbound HTTP guard (SSRF)

One guarded `http.Client` is used for every personal-server request: the
StreamableHTTP transport (`WithHTTPBasicClient`), the SSE transport, mcp-go's
OAuth handler, and our own server-side OAuth routes (metadata discovery,
dynamic client registration, token exchange). Today
`mcpclient/http_manager.go` uses the default client, which follows
redirects, honours `HTTPS_PROXY` and has no dial guard.

- `net.Dialer.Control` checks **the IP actually being dialled** (defeats DNS
  rebinding between check and connect).
- `Proxy: nil`.
- `CheckRedirect` refuses cross-origin redirects (Go forwards custom headers
  like `X-API-Key` across hosts; it strips only `Authorization`/`Cookie`).
- Size limits and timeouts.
- Denied: `0/8`, `10/8`, `100.64/10` (incl. `100.100.100.200`), `127/8`,
  `169.254/16`, `172.16/12`, `192.168/16`, `198.18/15`, `224/4`+; `::1`,
  `fc00::/7`, `fe80::/10`, `fd00:ec2::254`; IPv4-mapped IPv6; names like
  `metadata.google.internal`.
- OAuth metadata (`token_endpoint`, `registration_endpoint`) is controlled by
  the server's operator, so every server-side fetch goes through this client;
  the `auth_url` shown to the person must be `https:` (never `javascript:` or
  `data:`).

### Personal secrets

A new per-person secret store, AES-GCM bound to the user id (AAD), stored in
the same personal state dir (`secrets.json`). Managed from the person's own
Setup; values are write-only in the UI. Used by:

- personal MCP credential headers:
  `"headers": {"Authorization": {"secret": "LINEAR_API_KEY", "format": "Bearer {}"}}`
  resolved at connect time from **the chatting person's** personal secrets
  only. They are **never** injected into the shell environment: Code/project
  secrets become `$SECRET_<NAME>` in the shell (`secrets_tools.go`), so anyone
  who can run a command in the Code could print them, and files the agent
  writes are readable by everyone with access to the Code;
- optionally the agent's `$SECRET_<NAME>` in that person's own chats (open
  question 1).

### Resolution and the bridge

`resolveCodeMCPServer(sessionID, server, tool)` is the **first** check in the
bridge resolver, before the report-run branch and the workshop branch, and it
also serves Code-root report runs (`window.report.run` from a Code dashboard,
today resolved by `resolveReportRunMCPServer` in `report_run.go` straight
from the platform catalog). **A report run gets the Code's global selection
only, never anyone's personal servers**: a dashboard is shared by everyone with
access to the Code, and its viewer is not the person who wrote it. It applies
whenever the session is a Code session by durable facts (decision 4):

1. Person = the session's **pinned person** (see "Who a session belongs to"
   below), never the mutable session owner.
2. If `server` is one of the person's personal servers **and** enabled for
   this Code for this person: resolve from the personal store, token file in
   the personal `tokens/`, connection session `mcp-user-<hash(user)>`
   (connections are never shared between people or with the platform).
3. Else if `server` is in the Code's global selection: resolve exactly as
   today (`resolveSelectedMCPServer`, `_platform` login).
4. Else refuse. A name clash between a personal and a global server resolves
   to personal only when the person enabled it; the UI prevents clashes on
   add.

The turn-start tool list is built the same way: for a Code session the agent's
MCP clients come from this resolution, and any platform server name in the
Code's `workflow.json` that is not in its global selection is ignored.

**Unique internal names.** The codeexec registry, `mcpcache`, generated
code-exec packages, tool-schema caches and
`registry.ResolveConnectionSessionID` (which maps to `mcp-platform`) are all
keyed by server name, so a personal "linear" could be served the platform
"linear"'s cached tools or connection, or the reverse. Personal servers get an
internal name unique per person, `u_<hash8>__<name>`, used for every cache,
connection and generated package; the model and the UI see `<name>`. A
collision test covers it.

### Who a session belongs to (pinned person)

Today the session owner is last-writer-wins: `EventStore.SetSessionOwner`
overwrites it on every `trackActiveSession` / `claimAgentWorksChatSession`. If
any path let a second principal submit a turn into an existing session, the
owner would flip and the resolver would use that person's personal credentials
mid-session, including for background agents and retained CLIs that call the
bridge between turns.

- A Code session's person is **pinned durably when the session is created**
  (stored with the session record) and never changes.
- A turn into that session from any other authenticated principal is
  **refused**: live input, steering, a co-owner or admin posting into someone
  else's chat, synthetic relay turns.
- Personal servers and secrets resolve only when the pinned person equals the
  turn's authenticated principal (and, between turns, only for the pinned
  person's own sub-agents and background work, which take the pinned parent
  person).
- Slack/WhatsApp DM turns run as the **matched sender**, never the bot owner
  (`bot_owner`): an editor's DM to a Code's bot resolves the editor's servers,
  not the owner's.

Known residual: a Code session created before pins existed, and not marked in
memory, still resolves through the old chain until its first new turn pins it;
the deploy that introduces pins ends retained CLIs, so this only covers
sessions from before that deploy.

### `workflow.json` is protected in Code

Code's Folder Guard makes the whole root writable, so an editor, or a
prompt-injected instruction in any chat, could rewrite `workflow.json`,
including the global selection (which is meant to be owner/co-owner only).

- Add `workflow.json` (and `product.json`) to the Code guard's blocked writes.
- The global selection changes only through the role-checked API and the
  `update_project_mcp_server_selection` tool, which checks the chatting
  person's role on the Code.
- Personal enablement never lives there (see the personal store).

### Agent tools and skill

- The `mcp` feature on Code with an option `{personal: true}`:
  - `list_mcp_servers` shows the Code's global selection plus the chatting
    person's personal servers.
  - `search_mcp_catalog` (platform metadata as a source of URLs/OAuth info).
  - `install_mcp_server` / `add_mcp_server` add to **the chatting person's**
    personal store only, remote URLs only; they never change the platform
    catalog from a Code.
  - `remove_mcp_server` removes the person's own server.
  - `update_project_mcp_server_selection`: the global selection needs owner or
    co-owner; a person's personal enablement is theirs alone.
- The `code-mcp` skill (rendered from the shared template) gets a section on
  global vs personal servers and secrets.

### UI

Code Setup → Integrations → Apps:

- **Global:** the platform servers the Code uses (owner/co-owners choose).
- **Yours:** the viewing person's own servers: add (search or URL), connect,
  reconnect, remove, and on/off for this Code. Nobody sees anyone else's.

Setup → Secrets gains **Your secrets** (personal, write-only values) next to
the global secrets the Code selects.

### Lifecycle

- **Remove a personal server:** delete its token and client files, close the
  person's connection and its pooled entries in the codeexec registry and
  `mcpcache`, drop it from every Code's enablement.
- **Person loses access to a Code:** their `enabled.json` entry for it is
  dropped (and ignored meanwhile, since access is re-checked); their servers
  stay theirs.
- **Code deleted:** entries for it in every `enabled.json` are dropped;
  personal servers are untouched.
- **Account deleted/disabled:** disabled sessions cannot start; on delete the
  personal store is deleted.

### Cost

MCP calls are already recorded per session with the workspace path
(`recordMCPBridgeCall`), so they land on the Code's cost row, split by person.

### Catalog servers as your own; providers without registration (2026-09-29)

- `GET /api/me/mcp/catalog` lists the platform catalog's remote servers a
  person can add as their own: https, public, http/sse, no platform header
  credentials. `POST /api/me/mcp/servers {"catalog": "<name>"}` copies the URL
  and sign-in endpoints (scopes, `extra_auth_params`); the login is the
  person's own. A client ID/secret in the catalog entry is copied into the
  person's sealed client file, never into servers.json.
- Google Workspace (Gmail, Drive, Docs, Sheets, Slides, Calendar, Chat,
  People) and GitHub have no dynamic registration. Connect answers
  `needs_client_id` with the callback URL. The person then enters their OAuth
  app's client ID and secret. These are stored sealed in
  `clients/<internal>.json` and are read back by `personalMCPServerConfig`, so
  refreshes work as well as the first sign-in. The same file holds DCR
  clients, and a DCR client made for another callback registers again.
- Google issues a refresh token only with `access_type=offline` and
  `prompt=consent`. mcpagent's `OAuthConfig.ExtraAuthParams` adds them to the
  authorization URL, and flow-owned parameters cannot be overridden.
- The platform connect (`/api/oauth/start`) also takes `client_secret`.
- Later: an admin-provided shared OAuth app per deployment, so people skip
  creating their own. Today that means putting `client_id`/`client_secret` in
  the deployment catalog entry.

## Tests (end-to-end, not mocked)

1. A adds a public no-auth remote server (e.g. DeepWiki), enables it in Code
   X; A's chat lists and calls its tool.
2. B (an editor of X) cannot see or call A's server in B's own chat; B adds
   and uses their own.
3. The Code's global selection works for both A and B with the platform
   login, as in a Crew.
4. A's server is not callable from a Crew, a workflow, or a Code where A did
   not enable it.
5. An API-key server takes A's personal secret in A's chat; B's chat cannot
   resolve A's secret by name.
6. Refused: stdio commands; `http://127.0.0.1`; private, link-local and
   metadata IPs; a public name that resolves or redirects to one (including
   OAuth endpoints).
7. OAuth: A's token lands only in A's personal store.
8. A Code session with no identifiable person gets no personal servers or
   secrets.
9. **Restart fail-closed:** restart the server; a bridge call from an old
   Code session id is refused, never served from the platform catalog.
10. **Report run:** a Code dashboard's `window.report.run` reaches the Code's
    global selection only, never a personal server, and cannot reach a
    platform server outside the Code's global selection or anyone's personal
    server.
11. **Name collision:** a personal "linear" and the platform "linear" in the
    same process never share a connection, cached tool list or generated
    package.
12. **SSRF:** a DNS name that resolves to `127.0.0.1`, and a `302` to the
    metadata IP (both for the MCP URL and the OAuth endpoints): refused.
13. **At rest:** token files on disk are ciphertext; a hybrid-mode CLI reading
    them gets nothing usable.
14. **Pinned person:** an editor posting into the owner's session is refused;
    a DM from an editor resolves the editor's servers, not the owner's; the
    owner field changing cannot change whose credentials a session uses.
15. **Secret theft:** a personal server header naming a global or project
    secret is refused at add time and at connect time.
16. **workflow.json:** the agent (in an editor's chat or via an injected file)
    cannot write `workflow.json`; the global selection changes only through the
    role-checked API or tool.

## Open questions for the owner

1. **Personal secrets beyond MCP:** should the agent also be able to use a
   person's personal secrets as `$SECRET_<NAME>` in their own chats, or only
   for MCP credential headers for now?
2. **Project (shared) secrets in Code:** keep them (shared by everyone with
   access to the Code, as today), or replace them with personal + global only?
3. **Personal servers outside Code:** Code only for now (recommended), or
   also in the person's own Crews later?
