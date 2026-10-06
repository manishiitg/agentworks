[← integrations / integrations](index.md)

# PLAT-482 — An MCP added to a workflow, Relay, Crew or Code belongs to that place and is used by everyone with access to it

| Field | Value |
|---|---|
| State | open |
| Priority | - |
| Product | integrations |
| Area | integrations |
| Summary | phase 1 on `main`, needs a restart: workflow, Relay, Crew and Code connections are shared with everyone who has access, runs included (the Upwork run failed on the old owner-only rule). |

| Coordination | Value |
|---|---|
| State | fixed on `main` (phases 1 and 2); needs a restart and a re-run of the Upwork step. Credential storage and place-less chats are the only open parts |
| Date | 2026-10-04 |
| Owner | integrations |
| Related | PLAT-474/475 (OAuth), PLAT-477 (trigger_mcp_discovery) |

## Source

The Upwork workflow run failed at its first Upwork call with `{"success":false,"error":"active MCP user required"}`,
while the same call worked from the chat. The Upwork connection was added to the workflow but was a "private" one:
usable only while its owner is the caller, and a run's step session has no person.

## Where the private concept came from

28 Sep 2026: a design for the Code product (`docs/design/code_private_mcp.md`, commit `409572385`): Code is a
private workspace per person, so a person's own GitHub or Google login stays theirs. 29–30 Sep it became
"personal MCP", then "place MCP" attached to workflows and Crews with the same owner-only rule; 3 Oct the Vault
checkpoint added the wording "private connections run only for their owner". A Code-only idea was copied onto
workflows and Crews, where it does not fit.

## Decision (owner, 2026-10-04)

Two kinds only: **Vault** (global, governed by groups) and **specific to a place**. An MCP added to a workflow,
Relay, Crew or Code is used by everyone with access to that workflow, Relay, Crew or Code, in chats, Run mode,
schedules and step runs. See DECISIONS.md.

## Done (phase 1)

- `placeMCPUsableBy`: a person needs access to the place (workflow access, Crew access, the Code's owner); a
  session with no person is the place's own. `attachedMCPServersForRoot` and `placeMCPSignedInInternalNames` use
  it instead of "caller is the owner" and "the adder can still edit".
- A step, run or schedule finds its place from server-set session data (`SessionShellConfig.WorkflowPath` /
  `WorkingDir`, never client input) and resolves the place's attached connections through
  `resolvePlaceAttachedMCP` (call time) and the agent-construction inventory (`scopeAgentMCP`). A name the place
  does not have falls through to the platform and Vault servers; two matches of one plain name are refused, not
  guessed.
- The place's list shows every connection of the place; any editor of the place can remove one.
- Tests: resolution for a run step (plain, upper-case and internal name), another place's session, no place,
  removal, ambiguity, the access rule, the Code owner. The two old owner-only tests were rewritten to the new
  rule. The full cmd/server package has no failure that main does not already have.

## Done (phase 2: a connection lives only where it was added)

Owner, 2026-10-04: "if I add an MCP to a Code it is there for that Code only, not for Crew or workflows; the same for
a Crew and for a workflow."

- A session that belongs to a workflow, Relay, Crew or Code (`placeRootForSession`: a pinned Code, the session's
  server-set shell config, then the server-recorded project folder) is marked place-scoped. From such a session
  the resolvers and the agent inventory no longer look in a person's own store: only that place's attached
  connections and Vault resolve; a connection added to another place, or kept only in the person's store, is
  refused ("not attached to this workflow, Crew or Code").
- The agent tools follow the place: `list_mcp_servers` lists the place's connections (with who added them) under
  `connections`, `install_mcp_server` attaches the new connection to the chat's place (editors only),
  `remove_mcp_server` detaches it from the place for everyone, `trigger_mcp_discovery` discovers the place's
  connection.
- Chats outside any place keep using the person's own connections for now (they have no place to attach to).
- Tests: a place session never reaches a person's other connection while a place-less chat still does; lookup of a
  place connection by plain or internal name.

## Done (phase 2: wording, UI, names)

- Agent-facing text: tool descriptions and messages (`list_mcp_servers`, `install_mcp_server`, `add/edit/remove_mcp_server`,
  `trigger_mcp_discovery`, the Code `manage_my_mcp_servers`), the integration-discovery guidance, the `work-mcp`
  and `work-mcp-connections` skills and the Code prompt say a connection belongs to the place it was added to, is
  used by everyone with access to it, and acts as the account of whoever connected it. The "private" wording and
  the "Integrations > My MCPs" pointer are gone (it is Integrations > Plugins).
- UI: the Connected list shows every connection of the place as "Connected by <person>"; the help text says who
  can use it; prompts no longer say "private". Hook and test renamed `usePlaceMcpConnections`.
- Code names: `private_mcp_*.go` is now `personal_mcp_http.go` and `mcp_connection_tools.go`; `privateMCPByCatalog`,
  `lookupPrivateMCP`, `recordPrivateMCP`, `ensurePrivateMCP`, `savePrivateMCPOverlay` and friends are renamed
  (`personalMCP…`, `attachPersonalLoginToPlace`, `ensurePersonalMCP`, `saveUserMCPOverlay`).
- Checks: the full cmd/server package has no failure that main does not already have; the frontend suite has one
  failure (`gatewayConsoleShared`, which also fails without these changes).

## Done (follow-up, 2026-10-05)

- The Integrations tab is named **Connections** now (it was Plugins). Agent prompts (Work, Relay, Code), the
  `work-mcp` skill and tool messages say "Integrations → Connections", and two stale frontend source tests
  were updated. The Work and Relay prompts still said "Private MCP logins belong to the signed-in user"; they
  now say a connection belongs to the workflow or Relay and is used by everyone with access.

## Left

- Chats that belong to no place (an ordinary personal chat) still use the person's own connections, because they
  have no place to attach to. If they should have one, give a person's chat area a place of its own.
- Credentials stay in the adder's store; moving them into place-owned storage needs re-sealing and is not done.
- A connection added by someone who later leaves the place keeps working until an editor removes it.
- Not tried on the real Upwork run: needs a restart, then a re-run of the failed step.

## Register notes

[PLAT-482](plat-482.md), phase 1 on `main`, needs a restart: workflow, Relay, Crew
and Code connections are shared with everyone who has access, runs included (the Upwork run failed on the old
owner-only rule). Phase 2 removes the "private MCP" concept.
