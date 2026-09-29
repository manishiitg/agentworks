# A workflow's, Crew's or Code's own MCP connections

Status: built 2026-09-29 (ai-work-7e). Owner decisions from the same day.
**Code joined on 2026-09-29 (user: "use the same way in crew/work and code"):**
a Code is a place like a Crew, and the separate per-person "personal" model of
[code_private_mcp.md](code_private_mcp.md) is retired (see "Code" below).

## What it is

Someone who can edit a workflow, or who owns a Crew or a Code, adds an MCP server
there **with their own login**: their Gmail, Drive, GitHub, Asana and so on, from the same catalog and
sign-in apps as Code. From then on it is one more MCP server in that workflow or
Crew. Every chat and run there uses it, like any other server: web chats,
schedules, triggers, calls from other workflows, and Slack channels and DMs.

Owner decisions:

1. **It belongs to that one place.** A connection added in a Crew is only for
   that Crew. It never shows in the person's Code, in other Crews, or in workflows,
   and the same holds for a Code's connections.
2. **Everyone who uses the place uses the adder's login.** That includes Slack
   channels, where anyone in the channel can @mention the bot. The person
   accepts this in a warning before adding: "Everyone who can use this Crew,
   including every chat, schedule, trigger, workflow that calls it and Slack channel it
   answers in, can use GoogleGmail as you."
3. **No special treatment at runtime.** It joins the place's servers like any
   other: chat turns, step agents, and the workshop bridge.

## Rules

- **Who adds:** only someone who can edit the workflow (owner or writer) or
  owns the Crew or the Code. Checked when adding and **again on every use**: once they
  lose edit access, or their account is disabled, the connection is skipped
  and the UI shows it as paused.
- **Who removes:** the person who added it, or anyone who can edit the place.
  Removing it deletes that login.
- **Who sees it:** anyone who can read the place sees the list (name, whose
  login, signed in or not). Nobody sees tokens or secrets.
- **Where the grant lives:** in server-owned state
  (`<state>/personal-mcp/attachments.json`), written only by the add route for
  the caller. It is never in the editable `workflow.json`. An editor who types
  someone's internal server name into `selected_servers` gets nothing: the
  bridge refuses any personal-shaped name that is not one of this place's
  connections (`errPlaceMCPUnavailable`).

## Storage

The connection store is reused unchanged (catalog add, sign-in apps, sealed
tokens, SSRF guard) under a store id per **(person, place)**:
`place:<user id>:<root>`. So a Crew's Gmail login is separate from the same
person's Code Gmail, and two people's Gmail in one Crew have distinct server
names (`u<hash>__gmail`). A store id hashes the whole `place:...` string:
`sanitizeUserIDForPath` maps anything with a colon or slash to `default`, which
used to put every place in one store (fixed 2026-09-29; no place connections
existed yet). A server that needs an API-key header names a **project secret**
of that place (Setup > Secrets, read from the shared project secret store by
the place's root), like the rest of the Crew, Code or workflow.

Roots: `Workflow/<name>`, `Crew/<id>`, `_users/<owner>/Chats/Work/projects/<id>`
and `_users/<owner>/Chats/Code/projects/<id>`. Any path inside one (a run
folder) maps to its root (`placeRootOf`).

## Runtime

- Chat turns (`handleQuery`): a workflow-phase turn (`workflowPhaseFolder`), a
  Crew turn or a Code turn (`agentProfileRuntimeWorkspace`) merges
  `attachedMCPServersForRoot` into its servers and `RuntimeOverrides`. The
  chat's retained session fingerprint includes these connections
  (`ChatConnections`), so connecting one relaunches the CLI on the next
  message (resuming the conversation) instead of the old process answering
  "not registered by any connected server".
- Step agents: `step_based_workflow.PlaceMCPServers` (installed by the
  server) is added in both step config paths (`addPlaceMCPServers`).
- Bridge: workshop sessions and Code sessions (`resolveCodeMCPServer`) resolve
  personal-shaped names only from the place's own connections; a Code session
  also accepts the plain name (`googlegmail`).

## API

- `GET /api/mcp/place?workspace_path=`: list (read access).
- `POST /api/mcp/place {workspace_path, catalog}`: add with the caller's login
  (edit access).
- `POST /api/mcp/place/{name}/connect?workspace_path=`: the caller signs in to
  their own connection.
- `DELETE /api/mcp/place/{name}?workspace_path=&owner=`: remove.

## UI

`PlaceMcpSection`, the one screen for a Code (its MCPs tab), a Crew (top of the
MCP tab) and a workflow (Capabilities → MCP): the list with whose login each
uses, "Add with your login" (search, sign-in groups such as Google Workspace as
service chips, catalog, or a server that is not listed with an API-key header
from the project secrets), then the warning, then sign-in (with the own-OAuth-app
panel when a provider needs one), Sign in and Remove. Admins also see the
sign-in apps setup.

## Code

A Code is a place: `_users/<owner>/Chats/Code/projects/<id>`. Only its **owner**
adds or removes connections (a participant of a shared Code can use and list
them, like a Crew's viewers). The agent's `manage_my_mcp_servers` tool
(`list`, `connect`, `remove`) acts on the Code's connections; the attached
`code-mcp` skill (from `work-mcp-connections`) tells it how. Secrets are the
Code's project secrets, exactly like a Crew: the personal secret store is gone.

Migration (`personal_mcp_migrate.go`, at every start, idempotent): what a person
had switched on in a Code under the old model becomes that Code's connection,
with its login re-sealed for the new path (to the first Code only, since a
rotating refresh token must not live in two places; later Codes show "not
signed in yet") and header secrets copied into the Code's project secrets. A
switch in someone else's Code is not moved (logged). Nothing is deleted from
the old store.

## Not done

- Plain personal chats (no workflow, Crew or Code): not a place.
- Display names: the model sees `u<hash>__gmail` (a Code session also accepts
  the plain name).
- Cleaning up the old personal stores once the migration is verified.
- Participants of a shared Code connecting their own (owner-only for now).
