# MCP Connection Pooling and Session Tracking

The `SessionConnectionRegistry` combines two concerns:

- MCP subprocess connections are pooled by server under the `global`
  connection session, avoiding duplicate processes across agents.
- Logical agent/workflow session IDs scope pending server configuration,
  stopped-session protection, HTTP-to-MCP lifecycle tracking, and tool-call
  metadata.

The historical filename is retained because other documentation links here,
but browser state is not managed by this package.

## Connection flow

1. Agent setup loads the selected server configuration.
2. If a cached tool schema exists, the registry stores the configuration under
   the logical session and defers starting the subprocess.
3. The first tool call resolves the server's connection key to `global` and
   creates or reuses one client for that server.
4. Per-key mutexes prevent concurrent callers from spawning duplicate clients.
5. Broken connections are removed from the registry and retried once through
   the normal MCP cache/connection path.

## Lifecycle

- `RegisterHTTPSession` associates logical MCP sessions with an owning HTTP
  session so stop requests can mark work as stopped.
- Stopped-session checks reject late code-execution calls before they can
  resurrect a process after the user stopped a workflow.
- `CloseSessionServer` removes and closes one registered server connection.
- `CloseAllSessions` closes the global pool during process shutdown.

Agent/browser tab state, CDP profiles, and headless browser sessions belong to
the Builder's managed agent-browser runtime, not to `mcpagent`.
