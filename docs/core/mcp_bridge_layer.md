# 🌉 MCP Bridge Layer & Exposed APIs

The **MCP Bridge Layer** is a critical architectural component of Runloop. It acts as a universal proxy and translation layer, allowing external local CLI agents such as **Claude Code**, **Codex**, and **Pi** to access the orchestrator's loaded tools, virtual tools, and workspace capabilities.

## Architecture Overview

Instead of requiring every agent to manage its own MCP server connections, the orchestrator centralizes tool discovery, session management, and routing. External agents use a lightweight proxy binary called `mcpbridge` to communicate with the orchestrator's REST API.

1. **Local CLI Agent** (e.g., `claude-code`, `gemini`) spawns `mcpbridge`.
2. **mcpbridge** translates the stdio-based MCP protocol into HTTP REST calls.
3. **Orchestrator** processes the HTTP request, executes the tool (routing to Docker, external APIs, or the local Workspace), and returns the result.

---

## 🔌 Exposed APIs

The orchestrator exposes several API layers to facilitate this bridge and manage the workspace.

### 1. Tool Execution & Discovery (Bridge API)
These endpoints are consumed by `mcpbridge` and the web UI to interact with tools.

*   **`POST /api/mcp/execute`**
    *   The central routing endpoint. Accepts a JSON payload containing the `server_name`, `tool_name`, and `arguments`.
    *   Automatically routes the request to the correct underlying MCP server, virtual tool, or workspace capability.
*   **`GET /api/tools`**
    *   Returns a flattened, formatted schema of all available tools across all connected MCP servers and virtual tools.
*   **`GET /api/mcp-config` & `POST /api/mcp-config`**
    *   Retrieves and updates the active MCP server configuration (`mcp_servers.json`).
*   **`POST /api/mcp-config/discover`**
    *   Forces a manual discovery and reconnection to all configured MCP servers.

### 2. Workspace API (Port 8080)
The `workspace` container runs a dedicated Planner API that the orchestrator interfaces with for local file and system management.

*   **Document Management:**
    *   `GET /api/documents` / `POST /api/documents`
    *   `GET/PUT/DELETE /api/documents/*filepath`
    *   `GET /api/glob` (File discovery via glob patterns)
*   **Search & Semantic Intelligence:**
    *   `GET /api/search` (Lexical grep search)
    *   `POST /api/search/process-file`
*   **System & Execution:**
    *   `POST /api/execute` (Secure shell command execution within the workspace)
    *   `POST /api/upload` (File uploads)
---

## 🌎 Universal Stdio Gateway

The `mcpbridge` binary isn't just for Claude or Gemini—it serves as a **Universal Stdio Gateway**. 

Any external LLM agent, framework (LangChain, AutoGPT, CrewAI), or custom script that supports the Model Context Protocol (MCP) via `stdio` can use `mcpbridge` to access your entire tool ecosystem through a single connection.

### Why use the Centralized Bridge?
*   **Single Connection, 100+ Tools**: Connect to one "server" (`mcpbridge`) and immediately gain access to every tool configured in your orchestrator (Slack, Workspace, Browser, etc.).
*   **Centralized Auth**: Manage API keys and OAuth tokens in one place (the orchestrator) rather than in every individual agent script.
*   **Session Isolation**: Use the `MCP_SESSION_ID` environment variable to isolate tool execution and folder guards between different external agents.
*   **Monitoring**: Every tool call made via the bridge is logged and visible in the orchestrator's central dashboard.

### Configuration for External Frameworks
To connect an external agent, simply point it to the `mcpbridge` binary:

```bash
# Example: Launching a custom MCP-compatible agent
my-agent-cli --mcp-command "mcpbridge" --mcp-env "MCP_API_URL=http://localhost:8080/api"
```

---

## 🪶 Lean variant: the `agentsession` pattern (small, fixed tool sets)

Everything above describes the *full orchestrator* case: a Gin-backed API server with
potentially 100+ tools across arbitrary user-built workflows, where the model
discovers a tool's schema via `get_api_spec` and then calls it through
`execute_shell_command + curl` using `$MCP_CUSTOM`/`$MCP_AUTH`. That discovery
route exists **because** the full tool catalog is too large/dynamic to hand the
model natively every session.

Small standalone apps with a fixed tool set can use
`agent_go/internal/agentsession`, a **leaner variant of the same bridge**:

1. **No Gin server, no HTTP handlers in the app itself.** Tools are plain Go
   functions: `agentsession.Tool{Name, Description, Params, Handler}`, passed
   directly into `agentsession.New(ctx, agentsession.Config{Tools: [...]})`.
2. `agentsession.New` calls `agent.RegisterCustomTool(...)`, which publishes each
   handler into a **session-scoped in-process registry**, and starts (once per
   process, via `ensureSharedBridge`) a small in-process HTTP "executor" server
   (`agentsession.startExecutorServer`) exposing `/tools/custom/{name}`.
3. `mcpagent`'s own `bridgeTools` var (`mcpagent/agent/coding_agents_bridge.go`)
   is a **small, fixed, package-level list — exactly 4 entries**
   (`execute_shell_command`, `diff_patch_workspace_file`, `agent_browser`,
   `get_api_spec`) — pinned by `TestBridgeToolsList`. **It is shared across
   every consumer of the mcpagent module (including AgentWorks itself) — never
   add your own app's tool names to it.** An earlier version of family-server
   did exactly that (added `set_child_profile`, `celebrate`, `suggest_handoff`,
   etc. directly to `bridgeTools`), which broke the guardrail test and leaked
   family-server-specific tool names into every other consumer's sessions.
   Reverted.
   The correct, scoped mechanism is `mcpagent.WithAdditionalBridgeTools(names
   ...string)` — an `AgentOption` that exposes the named tools natively for
   *that one agent instance only* (stored in `Agent.additionalBridgeTools`,
   unioned with `bridgeTools` inside `BuildBridgeMCPConfig`, never touching the
   shared var). `agentsession.New` calls this automatically for every tool
   in `Config.Tools` — callers never need to touch mcpagent's own files at all.
4. `mcpagent.BuildBridgeMCPConfig()` combines `bridgeTools` +
   `Agent.additionalBridgeTools`, resolves each name to its real schema,
   writes the result to a private file named by `MCP_TOOLS_FILE` (one environment string over 128 KiB cannot start a process), and launches `mcpbridge`
   with that set for the session.
5. `mcpbridge` itself has **zero hardcoded tool names** — it reads its entire tool
   list from `MCP_TOOLS_FILE` at startup (see `cmd/mcpbridge/main.go`). For a `"custom"`
   type entry, a tool call becomes `POST {MCP_API_URL}/tools/custom/{name}` with an
   `X-Session-ID` header — landing back on the *same app process* that registered
   the handler in step 2, not a separate orchestrator.

**Net effect:** two hops (Claude Code → `mcpbridge` stdio → app's own executor
HTTP → Go handler), both fast/local, and the model calls the tool **directly by
name** — no schema discovery, no curl construction. This is more reliable for a
small tool set and is the intended pattern for small standalone apps, not a
workaround.

**The one thing to remember:** a new custom tool registered via
`agentsession.Config.Tools` is exposed automatically — no manual step needed,
and nothing to edit in the shared `mcpagent` module. If you're calling
`mcpagent.NewAgent` directly (not through `agentsession`), you must pass
`mcpagent.WithAdditionalBridgeTools(yourToolNames...)` yourself, or the tool
is registered (handler exists) but genuinely invisible to the model — a real,
easy-to-hit gap, not a permissions issue. Either way, never add app-specific
names to the shared `bridgeTools` var itself.

AgentWorks uses that scoped mechanism for the platform `read_image` executor in
both chat and workflow-agent construction. It is therefore a direct MCP bridge
tool for Muse, Codex, and the other coding CLIs when the session's tool policy
admits it. This does not enable a provider-native image or file tool: the call
still lands on AgentWorks' workspace-aware `read_image` implementation, which
selects its configured image-analysis provider independently of the calling
chat model.

### Multiple accounts for one MCP service

Configured server names are exact connection IDs used by project/workflow
selection and bridge routing. Base and overlay entries with the same name but
different explicit OAuth token files are preserved separately by the SDK merge:
the overlay retains its existing ID; the base receives `<name>-base` (with a
numbered suffix if needed). This preserves existing selections. Discovery exposes
both IDs without credentials. Verify account/workspace labels with the relevant
connection's read-only service tools; do not infer them from names or storage
locations. Crew and AgentWorks use the same merged configuration and routing.
