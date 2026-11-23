# mcpagent

Go runtime for AgentWorks agents, MCP connections and coding-agent sessions.

## Use the Go runtime

The `agent` package owns immutable agent definitions, runtime configuration and sessions. Assemble an `AgentDefinition` and `RuntimeConfig`, construct it with `NewAgentFromDefinition`, then use the Agent/Session methods for turns, results and closure. Session handles carry provider continuation state. The public surface is pinned by `agent/public_api_golden_test.go`.

Supporting packages include:

- `mcpclient`: MCP connections, configuration, OAuth and tool discovery.
- `llm`: provider integration and event adaptation.
- `events` and `observability`: runtime events and configured tracers, including LangSmith.
- `agent/codeexec`: session-scoped tool execution and the shell bridge.
- `cmd/mcpbridge`: the MCP bridge executable used by coding agents.

The application owns product permissions, files, secrets and provider-account selection. Supply those through the existing definition and runtime configuration; the runtime does not grant product access.

## Build and verify

```sh
go build ./...
make test-p0-resume
go test ./agent -run 'TestAgentPublicMethodSurface|TestPackageFunctionSurfaceDoesNotRegrow|TestAgentAndSessionCloseContractsMatch' -count=1
```

`make test-p0-resume` checks coding-agent continuation without provider credentials. Other integration tests may require their documented services or credentials. Library users import the Go packages directly.
