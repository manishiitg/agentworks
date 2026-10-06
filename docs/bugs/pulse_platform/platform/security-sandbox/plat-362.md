[← platform / security-sandbox](index.md)

# PLAT-362 — Agents could act as another session or workflow

| Field | Value |
|---|---|
| State | open |
| Priority | - |
| Product | platform |
| Area | security-sandbox |
| Summary | closes the path for an agent to act as another user's session or workflow. |

| Coordination | Value |
|---|---|
| State | Pushed to main and reviewed; local and RTS deployment pending (awaiting the user's go-ahead) |
| Date | 2026-09-27 |
| Owner | security-sandbox |
| Related subsystems | coding-agent-bridge, pulse-governance, human-decisions |
| Reviewed by | ai-work-7e session (three review rounds; final round approved) |

## Problem

Agent tool calls reach the server through the executor routes `/tools/...` and
`/s/{session_id}/tools/...`. Two layers of this trusted things the caller chose.

1. **Pulse platform tools** (`search_platform`, `ask_platform_crew`,
   `read_crew_calls`) and `answer_human_input_request` took `workspace_path`
   from the model's arguments and acted as that workflow's owner. A Pulse run
   of workflow A could act for workflow B as B's owner. A Pulse reviewer or
   scheduled run could also answer, and so apply, the decision it had
   proposed.
2. **The bridge itself (H1).** Every agent got one process-wide
   `MCP_API_TOKEN`. The session a call acted as came from the URL path, the
   `X-Session-ID` header or the body `session_id`, all chosen by the caller.
   Any agent could name another user's Builder chat or background tool
   session (tool-session IDs are guessable) and act as them.

## What was done

### Pulse tools act only for the calling session (builder `173826594`, `e1e179b80`, `6f697f2e3`)

- `pulseToolScope` takes the workflow and principal from the trusted calling
  session: the background tool-session registry, or the Builder chat's
  workshop, plus its event-store owner. It never uses `workspace_path` from
  the arguments, and never falls back to a workflow owner or the default user.
  The principal must still have access to the workflow.
- `answer_human_input_request` (`humanAnswerScope`): only a person in their own
  Builder chat can answer a decision. Background and review agents and
  `sched_`/`schedule-` sessions are refused. `AnsweredBy` comes from the
  bound claims.
- `ask_platform_crew` is refused in a scheduled run's own turns (Gate, Plan
  Drift, finalizer). Goal Work agents keep it behind the Run permission;
  Builder chats keep it.
- The direct Pulse reviewer start carries its session as the caller, so the
  bound `run_in_background` re-checks owner and access and refuses a foreign
  executor.
- `read_crew_calls` refuses a call with no recorded Crew owner instead of
  resolving it as the default user.
- Related Pulse fixes in the same round: owned steps a background agent never
  handed back are stopped on exit, and completions deferred while a session
  was claimed are re-delivered when the claim is released.

### Per-session bridge tokens (H1) (builder `f48d158a4`, `4be86220e`, `b1a08dbee`; mcpagent `67371a9`, `9b217a9`; multi-llm `f77e6fc`)

- **Token.** Each agent gets a token for its own session:
  `mcps1.<base64url(session)>.<base64url(HMAC-SHA256(key, session))>`
  (`pkg/common/bridge_token.go`).
  - The key comes from a secret held only in the server's memory. It is random
    per start, or `MCP_BRIDGE_TOKEN_SECRET` if an operator pins it; that is
    read once, then removed from the environment.
  - It is never derived from the API token. The first version was, and review
    found that any process inheriting the server environment could then mint
    any session's token (`4be86220e`).
- **Server check.** `bridgeAuthMiddleware` (`cmd/server/bridge_auth.go`)
  guards both route groups.
  - A path, `X-Session-ID` or `X-Virtual-Scope-ID` naming another session
    gets a 403.
  - `X-Session-ID` and the request context are pinned to the token's session,
    so a body `session_id` cannot override it.
  - The process-wide token is refused. `AGENTWORKS_BRIDGE_ALLOW_GLOBAL_TOKEN=1`
    re-admits it as a logged rollback switch; it needs a restart, which also
    rotates a random secret.
- **Hand-offs.** Every agent environment gets only its own session's token.
  - `PopulateMCPBridgeShortEnv` derives `MCP_API_TOKEN`/`MCP_AUTH` from
    `MCP_SESSION_ID`. This covers steps, background agents, session changes,
    report runs and `native_shell` profiles.
  - Coding-CLI bridges mint from the agent's session at launch through the
    mcpagent `BridgeTokenForSession` hook. Agents with their own executor
    token, such as `internal/agentsession`, keep it.
  - `execute_shell_command` binds the session, the `/s/<id>` URL and the token
    to the trusted session only (`sessionIDFromContext`), never to the
    model-supplied `extra_env`; a shell with no trusted session gets no token.
    Review finding A: `extra_env` could name a victim session and have its
    token minted. Report runs and scripted steps now pass their session
    through the request context.
  - The server no longer exports `MCP_API_TOKEN` and removes
    `MCP_SERVER_API_TOKEN` after reading it. Workspace shells also drop
    `MCP_SERVER_API_TOKEN` and `MCP_BRIDGE_TOKEN_SECRET`.
- **Virtual tools (finding B).** A session's virtual-tool call no longer falls
  back to the global virtual tools. Those are closures over whichever agent
  registered last, so the fallback ran another session's `search_large_output`
  and `get_api_spec`.
- **Execute routes (finding C).** `/api/mcp/execute` and `/api/virtual/execute`
  took the session from the body behind only a login, so any signed-in user
  could use another user's MCP servers. A named session must now belong to the
  caller. Calls without a session, such as the MCP tool tester, are unchanged.
- **Pi.** The Pi MCP config fingerprint drops the per-session token (multi-llm
  `f77e6fc`). Otherwise two same-profile Pi agents in one working directory
  failed the lease with "different MCP configs".

## Verification

- **Unit tests** cover:
  - token mint and verify, forged and other-key tokens;
  - path, header and virtual-scope refusal;
  - the global token refused, and the rollback switch;
  - the pinned secret removed from the environment;
  - `extra_env` naming a victim, and a sessionless shell getting no token;
  - no global virtual fallback;
  - owner checks on the execute routes;
  - the Pi fingerprint;
  - Pulse scope, decision answers and Crew-call attacks.

  Full `cmd/server/...`, step, workspace, mcpagent and picli suites pass,
  except four failures that already fail on main without these changes
  (`TestSalesCrewCatalogHasInstallableRoles`,
  `TestLoadPlaybookCatalogFindsEngineeringPlaybooks`,
  `TestSearchPlaybooksReturnsWebsiteGrowthTeamProposal`,
  `TestAssembledWorkflowPrompt`).
- **Live, on an isolated server** (ports 18843/18844) with real coding CLIs:
  - `workflow-auto-notification-e2e` (real MCP-bridge file operation) passes
    for Claude Code, Codex and Muse.
  - `coding-agent-chat-e2e` retained-window P0 passes for Codex and Claude
    Code.
  - No 401/403 on the tool routes, and no virtual-tool session misses.
  - The spawned `mcpbridge` carries only its `mcps1` session token: no server
    token, no secret.
  - The global token and missing tokens get a 401.

## What is left

**Deployment**
- [ ] Deploy to RTS with the user's go-ahead (ai-work-7e deploys).
  - In order: mcpagent `67371a9` + `9b217a9`, multi-llm `f77e6fc`, builder
    through `b1a08dbee`.
  - Restart the local server.
  - Watch for `[BRIDGE_AUTH]` refusals and 401s on `/tools` after the restart.
- [ ] Coding CLIs still running in tmux from before a restart hold a token that
  no longer verifies. The startup `mlp-*` sweep removes them; pin
  `MCP_BRIDGE_TOKEN_SECRET` if survivors must keep working.

**Not verified live**
- [ ] Pi: failed on its own tool-search layer and a rate limit, with no auth
  errors; not rerun on unchanged main to prove it fails there too. The Pi
  same-directory concurrency fix is unit-tested only.
- [ ] Cursor: not logged in on the test machine.
- [ ] Codex adapter P0: a startup timeout with codex 0.157.1 (unrelated to this
  change, in the adapter's own test).

**Follow-ups from the review (not deploy blockers)**
- [ ] **D1: partly done; the rest is tracked in [PLAT-364](plat-364.md).**
  - Done on RTS 2026-09-28 (e59220636): Landlock no longer grants `/tmp` (only
    `/tmp/.agent-browser`), HOME moved to `<workflow|Crew>/.sandbox-cache/home`,
    and leaked `/tmp` dotfiles and credentials were removed.
  - Still open (verified on RTS): Landlock does not cover `connect()` on
    pathname Unix sockets, so a sandboxed command can still reach
    `/tmp/tmux-<uid>/default`. Coding CLIs also run unsandboxed, so their
    native Read tools are unconfined.
  - Original finding: the Landlock shell has read-write `/tmp`. Other sessions' bridge
  configs there hold their tokens (Claude structured MCP configs, Pi/Muse
  bridge configs), and the tmux socket `/tmp/tmux-<uid>/default` lets a shell
  capture or type into other users' CLIs.
  Fix: a private `TMPDIR` per shell, bridge configs in per-session 0700
  directories outside grants, tmux on a dedicated `-S` socket, and Landlock
  socket scoping on ABI ≥ 6.
  - Same class: the D2 token files (0600, user cache dir) are still owned by
    the one OS user every agent runs as. An agent's native (non-Landlock)
    Read tools can read other sessions' files. Needs per-session isolation,
    not just file modes.
- [x] **D5 (builder, this commit).** Dismiss and reconnect now require
  `canAccessTerminalSession`: the owner, an admin for ownerless sessions, or a
  workflow writer for bot sessions. `GET /api/browser/sessions` lists only the
  caller's own sessions' browsers; admins still see all. A CDP owner that is
  a per-Crew or per-workflow browser name (not a session ID) is shown when it
  matches the browser of a session the caller can see.
- [x] **D2 (multi-llm `c46a5df`, mcpagent `b0cdf7b`).** Cursor's
  `.cursor/mcp.json` no longer holds the token.
  - Each bridge token goes to a private 0600 file in the user's cache
    directory, and the config names it with `MCP_API_TOKEN_FILE`, which
    `mcpbridge` reads.
  - The structured path deletes the file after the call; the tmux path keeps
    it for the session.
  - Each backend process keeps its files in its own folder (named by PID);
    startup removes the folders of backends that have exited, next to the
    tmux orphan sweep, and never touches a live backend's (another server on
    the same machine). File names are a hash of the token, never the session
    ID.
  - Not live-tested: Cursor is not logged in on the test machine.
- [x] **D3 (mcpagent `b0cdf7b`).** The global MCP clients belong to whichever
  agent registered last, with that user's credentials. A session call with no
  connection of its own no longer falls back to them; it uses its own session
  connection or the shared-config cache.
  - Availability is unchanged: the scope check still passes exactly when the
    old code would have found a global client.
  - Only the credential source changes: shared config, never another agent's.
  - Follow-up: calls without a session (the MCP tool tester on
    `/api/mcp/execute`, open to any signed-in user) no longer use the global
    clients either; they use the shared-config connection.
- [ ] **D4.** `canUseSessionIDForQuery` lets a user pre-claim predictable
  untracked session IDs (`wfask-<sha>`, `work:project:<id>`, ...). Not a small
  fix: it needs each feature's ID scheme and who it belongs to.
- [ ] **D6.** Tokens never expire or get revoked for a pinned secret. A random
  secret per start acts as the epoch today. Consider an epoch, refusing tokens
  of stopped sessions, and redacting `mcps1.` in tool output and transcripts.
- [ ] **D7.** `internal/agentsession` keeps the old single-token model, if any
  product hosts it multi-user.
- [ ] Strip `SECRET_*`, `DB_PATH` and `STEP_*` from model-supplied `extra_env`
  (folder guard still governs file access).
- [ ] Dev and CI harnesses that run the orchestrator in-process against a live
  server never set the signing secret, so the live server refuses them.
  Wire the secret in, or set the rollback switch in those harnesses.

## Register notes

[PLAT-362](plat-362.md) closes the path for an
agent to act as another user's session or workflow. Pulse platform tools and
decision answers now act only for the calling session's own workflow and owner.
Each agent's tool calls carry a token for its own session, signed with an
in-memory secret, and the server refuses any request that names another
session through the path, header, virtual-tool scope or model-supplied shell
env. The body-session execute routes now require the caller's own session.
Reviewed and pushed to main. Deployment, Pi/Cursor live checks, and follow-ups
D1–D7 (shared `/tmp` and tmux socket, unowned session routes, Cursor config in
the workspace, and more) are listed in the ticket.
