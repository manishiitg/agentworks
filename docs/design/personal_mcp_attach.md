# A workflow's or Crew's own MCP connections

Status: built 2026-09-29 (ai-work-7e). Owner decisions from the same day.
Related: [code_private_mcp.md](code_private_mcp.md) (Code's per-person servers).

## What it is

Someone who can edit a workflow or a Crew adds an MCP server there **with their
own login**: their Gmail, Drive, GitHub, Asana and so on, from the same catalog and
sign-in apps as Code. From then on it is one more MCP server in that workflow or
Crew. Every chat and run there uses it, like any other server: web chats,
schedules, triggers, calls from other workflows, and Slack channels and DMs.

Owner decisions:

1. **It belongs to that one place.** A connection added in a Crew is only for
   that Crew. It never shows in the person's Code, in other Crews, or in workflows.
   Code keeps its own strictly personal servers (only the chatting person's).
2. **Everyone who uses the place uses the adder's login.** That includes Slack
   channels, where anyone in the channel can @mention the bot. The person
   accepts this in a warning before adding: "Everyone who can use this Crew,
   including every chat, schedule, trigger, workflow that calls it and Slack channel it
   answers in, can use GoogleGmail as you."
3. **No special treatment at runtime.** It joins the place's servers like any
   other: chat turns, step agents, and the workshop bridge.

## Rules

- **Who adds:** only someone who can edit the workflow (owner or writer) or
  owns the Crew. Checked when adding and **again on every use**: once they
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

The personal store is reused unchanged (catalog add, sign-in apps, sealed
tokens, SSRF guard) under a store id per **(person, place)**:
`place:<user id>:<root>`. So a Crew's Gmail login is separate from the same
person's Code Gmail, and two people's Gmail in one Crew have distinct server
names (`u<hash>__gmail`). Servers that need API-key headers aren't supported
here yet (headers come from personal secrets, which have no UI in a place):
only sign-in (OAuth) and open servers.

Roots: `Workflow/<name>`, `Crew/<id>`, `_users/<owner>/Chats/Work/projects/<id>`.
Any path inside one (a run folder) maps to its root (`placeRootOf`).

## Runtime

- Chat turns (`handleQuery`): after the Code branch, a workflow-phase turn
  (`workflowPhaseFolder`) or a Crew turn (`agentProfileRuntimeWorkspace`) merges
  `attachedMCPServersForRoot` into its servers and `RuntimeOverrides`.
- Step agents: `step_based_workflow.PlaceMCPServers` (installed by the
  server) is added in both step config paths (`addPlaceMCPServers`).
- Bridge (workshop sessions): personal-shaped names resolve only from the
  place's own connections.

## API

- `GET /api/mcp/place?workspace_path=`: list (read access).
- `POST /api/mcp/place {workspace_path, catalog}`: add with the caller's login
  (edit access).
- `POST /api/mcp/place/{name}/connect?workspace_path=`: the caller signs in to
  their own connection.
- `DELETE /api/mcp/place/{name}?workspace_path=&owner=`: remove.

## UI

`PlaceMcpSection`, at the top of the Crew MCP tab and the workflow's
Capabilities → MCP (apps): the list with whose login each uses, "Add with your
login" (catalog picker, then the warning, then sign-in), Sign in and Remove.

## Not done

- API-key (header) servers in a place.
- Plain personal chats (no workflow or Crew): not a place.
- Display names: the model sees `u<hash>__gmail` (same open item as Code).
