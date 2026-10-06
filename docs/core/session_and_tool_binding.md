# Session And Tool Binding

This doc explains how `session_id` works in the current runtime.

The important part is that there is not just one session concept.
The system uses multiple session identifiers for different layers:

- HTTP/chat session identity
- MCP connection-sharing session identity
- browser session identity
- provider CLI resume identity

If those are conflated, behavior looks random. In code, they are intentionally different.

## Why This Exists

Tools need stable session-scoped state for things like:

- conversation history
- event streaming
- shell working directory
- folder guard restrictions
- browser reuse
- MCP server connection reuse
- Coding-agent native resume

The current architecture binds different parts of tool state to different session IDs depending on what is being shared.

## The Main Session Types

### 1. HTTP session ID

This is the user-facing session ID passed through request/response APIs and `X-Session-ID`.

It is the primary session identity for:

- conversation history
- event streaming and polling
- stop / clear session APIs
- session-scoped shell config in `common.sessionShellConfigs`
- workflow stop cleanup via `mcpagent.CloseHTTPSession`
- Coding-agent resume caches

Relevant code:

- [server.go](../../agent_go/cmd/server/server.go#L926)
- [server.go](../../agent_go/cmd/server/server.go#L2681)
- [types.go](../../agent_go/pkg/common/types.go#L67)

### 2. MCP session ID

This is the connection-sharing session used by MCP agents and code-exec HTTP tool calls.

Its job is:

- reuse MCP server connections across agents
- preserve stateful MCP connections when a server requires them
- let workflow group runs use different tool sessions while still belonging to one parent HTTP session

Relevant code:

- [base_orchestrator.go](../../agent_go/pkg/orchestrator/base_orchestrator.go#L282)
- [workflow_orchestrator.go](../../agent_go/pkg/orchestrator/types/workflow_orchestrator.go#L733)
- [base_orchestrator_agent_factory.go](../../agent_go/pkg/orchestrator/base_orchestrator_agent_factory.go#L165)
- [llm_agent.go](../../agent_go/pkg/agentwrapper/llm_agent.go#L274)

### 3. Browser session ID

This is the browser identity used by browser tools when the tool asks for `session="default"` or another browser session name.

It exists so:

- multiple agents can intentionally share one browser
- workshop groups can reuse one stable browser per group
- isolated sub-agents can get separate browser state

The browser layer can remap `"default"` to a deterministic shared browser session.

Relevant code:

- [types.go](../../agent_go/pkg/common/types.go#L127)
- [executor.go](../../agent_go/pkg/browser/executor.go#L82)
- [controller.go](../../agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller.go#L283)

### 4. Provider CLI resume IDs

These are separate from both HTTP and MCP sessions.

Current cached provider resume state:

- `claudeCodeSessionIDs[httpSessionID]`
- `geminiSessionIDs[httpSessionID]`
- `geminiProjectDirIDs[httpSessionID]`

They are used to resume CLI-based providers on later turns of the same HTTP session.

Relevant code:

- [server.go](../../agent_go/cmd/server/server.go#L5289)
- [server.go](../../agent_go/cmd/server/server.go#L5545)

## Shell Tools

`execute_shell_command` resolves session-scoped behavior from `ChatSessionIDKey`.

It uses that key to look up `SessionShellConfig`, which currently stores:

- `WorkingDir`
- `ReadPaths`
- `WritePaths`
- `GeminiProjectDirID`
- `BrowserMode`
- `BrowserSessionID`

Relevant code:

- [types.go](../../agent_go/pkg/common/types.go#L55)
- [execute_shell_command.go](../../agent_go/pkg/workspace/execute_shell_command.go#L80)

That means shell behavior is effectively bound to the session currently attached to `ChatSessionIDKey`.

In practice that controls:

- default cwd
- folder sandbox
- Gemini relative-path rewriting
- browser-related shell guidance

## Browser Tools

`agent_browser` uses both:

- `ChatSessionIDKey` for the agent-level session
- `WorkflowSessionIDKey` for the root workflow/chat session

Delegated agents inherit the workflow browser session. The two keys remain
useful because browser accounting and cleanup are workflow-scoped even when a
tool request already carries an agent-level session.

Relevant code:

- [workspace_browser_tools.go](../../agent_go/cmd/server/virtual-tools/workspace_browser_tools.go#L39)
- [executor.go](../../agent_go/pkg/browser/executor.go#L67)

## HTTP Tool Calls From Code Execution Mode

Code execution mode uses session-aware tool URLs.

The server exposes:

- global routes at `/tools/...`
- session-scoped routes at `/s/{session_id}/tools/...`

When a session-aware workspace executor is created, it injects:

- `MCP_API_URL={base}/s/{session_id}`
- `MCP_SESSION_ID={session_id}`

So generated code can call:

- `$MCP_API_URL/tools/mcp/{server}/{tool}`
- `$MCP_API_URL/tools/custom/{tool}`

without manually threading `session_id` through every request body.

Relevant code:

- [server.go](../../agent_go/cmd/server/server.go#L926)
- [workspace_advanced_tools.go](../../agent_go/cmd/server/virtual-tools/workspace_advanced_tools.go#L112)

## MCP Connection Reuse

Every agent created by an orchestrator receives the orchestrator's MCP session ID.

That is how:

- one workflow shares its configured server connections across steps
- one group run can keep its own MCP state
- sub-agents can share or isolate MCP state depending on the session ID passed to them

The core rule is:

- same MCP session ID means shared MCP connection state
- different MCP session ID means isolated MCP connection state

Relevant code:

- [base_orchestrator.go](../../agent_go/pkg/orchestrator/base_orchestrator.go#L282)
- [llm_agent.go](../../agent_go/pkg/agentwrapper/llm_agent.go#L274)

## Workflow And Workshop Behavior

### Workflow execution

Step-based workflow execution creates a session-group MCP session ID and uses it for agent/tool connection sharing.

The parent HTTP session is tracked separately so a stop action can close all derived MCP sessions.

Relevant code:

- [controller.go](../../agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller.go#L217)
- [workflow_orchestrator.go](../../agent_go/pkg/orchestrator/types/workflow_orchestrator.go#L740)

### Workshop group switching

Workshop mode can switch from the placeholder MCP session to a stable per-group MCP session.

When that happens, the code also:

- registers the group session under the parent HTTP session
- copies folder guard from the parent HTTP session
- binds the group MCP session to a stable browser session

Relevant code:

- [controller.go](../../agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller.go#L303)

### Browser binding for workshop groups

Workshop groups can share a deterministic browser session even while their MCP/tool session changes.

That is handled by:

- `mcpagent.RegisterBrowserSessionOverride(...)`
- `common.SetSessionBrowserSessionID(...)`

This lets tool calls using `"default"` converge onto one stable group browser.

Relevant code:

- [controller.go](../../agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller.go#L283)

## Sub-Agent Browser Sessions

Sub-agents reuse the parent workflow browser session. Folder guards, workflow
DB bindings, and other tool permissions still use dedicated tool sessions where
required; that security isolation does not create or advertise a second browser
ownership mode. Distinct browser identities use explicitly configured CDP
profiles or browser session names rather than a delegation flag.

Relevant code:

- [workspace_browser_tools.go](../../agent_go/cmd/server/virtual-tools/workspace_browser_tools.go#L42)

## What Survives A New Turn

Within the same running server process, reusing the same HTTP session can preserve:

- conversation history
- event history
- stored agent instance in some modes
- Claude Code CLI resume ID
- session shell config

That is why later turns in the same session can continue using the same chat context and provider CLI resume state.

## What `stop session` Does

`/api/session/stop` currently:

- marks the session stopped
- cancels active agent/workflow contexts
- closes workshop sessions
- calls `mcpagent.CloseHTTPSession(sessionID)`
- kills tracked headless browser sessions
- preserves conversation history

Relevant code:

- [server.go](../../agent_go/cmd/server/server.go#L5645)

Important current behavior:

- stop closes live MCP/browser activity
- stop does **not** clear conversation history
- stop does **not** currently call `ClearSessionShellConfig`

So stop is a runtime cancel/cleanup operation, not a full state reset.

## What `clear session` Does

`/api/session/clear` currently clears:

- conversation history
- workflow objective cache
- Claude Code resume ID
- Gemini resume ID
- Gemini project dir ID
- tracked headless browser sessions

Relevant code:

- [server.go](../../agent_go/cmd/server/server.go#L5855)

Important current behavior:

- clear resets chat/provider resume state, but it is not the same cleanup path as stop
- clear currently does **not** call `mcpagent.CloseHTTPSession`
- clear still does **not** currently call `ClearSessionShellConfig`

## What A Server Restart Resets

Most live tool binding state is in memory.

A server restart resets things like:

- `common.sessionShellConfigs`
- in-memory MCP session registrations
- browser session tracker
- Claude Code resume IDs
- Gemini resume IDs
- Gemini project dir IDs
- live agent instances
- workflow runtime/session tracking maps

What can still come back after restart:

- persisted events
- persisted conversation history
- workflow files and run artifacts

What does **not** automatically come back after restart:

- live tool bindings
- MCP connection reuse state
- shell cwd/folder-guard map entries
- browser session overrides
- provider CLI resume state

So if the user means "can tool permissions/session binding survive restart?", the current answer is:

- no for live session-bound tool state
- yes only for state that is rebuilt from files or database-backed history

## Practical Mental Model

Use this model:

- HTTP session ID = top-level user/chat/workflow session
- MCP session ID = shared tool-server connection identity
- browser session ID = actual browser instance identity
- provider CLI session IDs = per-provider resume handles

And:

- shell sandboxing is keyed off the current chat session context
- MCP connection reuse is keyed off the current MCP session
- browser reuse may be remapped independently of the MCP session
- server restart drops the live bindings because they are in-memory

## Related Docs

- [mcp_bridge_layer.md](mcp_bridge_layer.md)
- [folder_guard_system.md](folder_guard_system.md)
- [browser.md](browser.md)
- [workflow_builder_interactive.md](../workflow/workflow_builder_interactive.md)
- [workflow_shell_working_directory.md](../workflow/workflow_shell_working_directory.md)
