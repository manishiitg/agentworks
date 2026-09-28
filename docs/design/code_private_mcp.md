# MCP servers in Code: global and personal

Status: design, for review. Not built. Parent design:
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
resolves another person's personal secret.

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
4. **Fail closed.** If a Code session's person or Code cannot be identified,
   it gets no personal servers and no personal secrets (global ones still
   follow the Code's selection).
5. **Admin inspection** shows which personal servers a person enabled in a
   Code (name, URL, connected), never tokens or secret values; audited.

## Design

### Personal store (server-owned, outside every workspace)

```
<AGENTWORKS_STATE_ROOT>/personal-mcp/<sha256(user)[:32]>/   (0700)
  servers.json        # name, url, transport, oauth metadata, header refs
  tokens/<server>.json
  clients/<server>.json   # dynamic client registrations
```

- Never inside a Code or the user's workspace tree: the agent and anyone with
  file access to a Code can read its files. The Landlock read set is an
  allowlist that never includes this directory; the browser workspace proxy
  cannot reach it (not under the docs root).
- Per-Code enablement lives with the Code, keyed by person, and holds no
  secrets: `workflow.json` capabilities gain
  `personal_servers: { "<user>": ["linear", ...] }` (names only).

### Personal secrets

A new per-person secret store, AES-GCM bound to the user id (AAD), stored in
the same personal state dir (`secrets.json`). Managed from the person's own
Setup; values are write-only in the UI. Used by:

- personal MCP credential headers:
  `"headers": {"Authorization": {"secret": "LINEAR_API_KEY", "format": "Bearer {}"}}`
  resolved at connect time from **the chatting person's** personal secrets
  (then global secrets, if the Code selected that global secret);
- optionally the agent's `$SECRET_<NAME>` in that person's own chats (open
  question 1).

### Resolution and the bridge

`resolveCodeMCPServer(sessionID, server, tool)` runs first in the bridge
resolver whenever `common.CodeSessionRoot(sessionID)` is set:

1. Person = the session owner (already tracked for every chat; inherited by
   sub-agents and background work).
2. If `server` is one of the person's personal servers **and** enabled for
   this Code for this person: resolve from the personal store, token file in
   the personal `tokens/`, connection session `mcp-user-<hash(user)>`
   (connections are never shared between people or with the platform).
3. Else if `server` is in the Code's global selection: resolve exactly as
   today (`resolveSelectedMCPServer`, `_platform` login).
4. Else refuse. A name clash between a personal and a global server resolves
   to personal only when the person enabled it; the UI prevents clashes on
   add.

The turn-start tool list is built the same way. Every personal connect
re-checks the URL rules.

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
  person's connection, drop it from every Code's enablement.
- **Person loses access to a Code:** their enablement entry is dropped; their
  servers stay theirs.
- **Code deleted:** its enablement map goes with `workflow.json`; personal
  stores are untouched.
- **Account deleted/disabled:** disabled sessions cannot start; on delete the
  personal store is deleted.

### Cost

MCP calls are already recorded per session with the workspace path
(`recordMCPBridgeCall`), so they land on the Code's cost row, split by person.

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

## Open questions for the owner

1. **Personal secrets beyond MCP:** should the agent also be able to use a
   person's personal secrets as `$SECRET_<NAME>` in their own chats, or only
   for MCP credential headers for now?
2. **Project (shared) secrets in Code:** keep them (shared by everyone with
   access to the Code, as today), or replace them with personal + global only?
3. **Personal servers outside Code:** Code only for now (recommended), or
   also in the person's own Crews later?
