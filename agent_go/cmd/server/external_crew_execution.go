package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path"
	"sync"
	"time"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
	"github.com/manishiitg/mcpagent/executor"
	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

var crewExecutionMembers = map[string]bool{
	"execute_shell_command": true, "agent_browser": true,
	"mutate_workflow_db": true, "apply_workflow_db_migration": true, "create_workflow_database_snapshot": true,
	"call_selected_mcp_tool": true,
}

// Entries live only while calls use them. Unrelated sessions never wait on a
// shared stripe, and a disconnected waiter can leave without starting work.
var crewExecutionLocks = struct {
	sync.Mutex
	entries map[string]*crewExecutionLock
}{entries: map[string]*crewExecutionLock{}}

type crewExecutionLock struct {
	permit chan struct{}
	users  int
}

func lockCrewExecution(ctx context.Context, session string) (func(), error) {
	crewExecutionLocks.Lock()
	entry := crewExecutionLocks.entries[session]
	if entry == nil {
		entry = &crewExecutionLock{permit: make(chan struct{}, 1)}
		crewExecutionLocks.entries[session] = entry
	}
	entry.users++
	crewExecutionLocks.Unlock()
	drop := func() {
		crewExecutionLocks.Lock()
		entry.users--
		if entry.users == 0 {
			delete(crewExecutionLocks.entries, session)
		}
		crewExecutionLocks.Unlock()
	}
	select {
	case entry.permit <- struct{}{}:
		return func() { <-entry.permit; drop() }, nil
	case <-ctx.Done():
		drop()
		return nil, ctx.Err()
	}
}

func crewExecutionDefinitions() []llmtypes.Tool {
	tools := append(workspace.GetAdvancedToolDefinitions(), virtualtools.CreateWorkspaceBrowserTools()...)
	tools = append(tools, virtualtools.CreateWorkflowDBToolRegistry("", "", "").Tools...)
	tools = append(tools, llmtypes.Tool{Type: "function", Function: &llmtypes.FunctionDefinition{
		Name: "call_selected_mcp_tool", Description: "Call a tool from this Crew's attached MCP connection or selected governed server. Discover attached tools with crew_mcp action=discover and diagnostics. Supply only the returned server and tool names plus the tool's arguments; live connection, selection and Vault checks apply. No other project's or personal unattached connection is reachable.",
		Parameters: llmtypes.NewParameters(map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"server": externalString("Exact attached or selected server name."), "tool": externalString("Exact tool name."), "args": map[string]any{"type": "object"}}, "required": []string{"server", "tool", "args"}}),
	}})
	selected := []llmtypes.Tool{}
	for _, tool := range tools {
		if tool.Function != nil && crewExecutionMembers[tool.Function.Name] {
			selected = append(selected, tool)
		}
	}
	return selected
}

func crewExecutionSchema(tool llmtypes.Tool) (map[string]any, error) {
	raw, err := json.Marshal(tool.Function.Parameters)
	if err != nil {
		return nil, err
	}
	var schema map[string]any
	if err = json.Unmarshal(raw, &schema); err != nil {
		return nil, err
	}
	schema["additionalProperties"] = false
	if tool.Function.Name == "execute_shell_command" {
		schema["properties"].(map[string]any)["timeout"] = map[string]any{"type": "integer", "minimum": 1, "maximum": 25, "description": "Maximum runtime in seconds; default 25. Use a background task for longer work."}
	}
	return schema, nil
}

func externalCrewExecutionDefinition(add func(string, string, bool, bool, map[string]any, ...string)) error {
	if err := externalCrewNativeDefinition(add, "crew_execution", "Execute a shared Crew shell, browser, selected MCP or managed database tool directly, without a model turn. action=history returns the last 100 owner-only audit records. action=list returns the enabled tools' descriptions and exact argument schemas; otherwise action is that tool name and arguments matches its schema. Requires crews:write, current editable account and Crew ownership. Uses this Crew's sandbox and connection file limits; never borrows a chat's authority. Shell timeout defaults to 25 seconds. Browser, connected MCP and database operations require unrestricted Crew file consent; credentials remain in Integrations. Database reads use query_database.", crewExecutionDefinitions()); err != nil {
		return err
	}
	tools, err := crewIntegrationDefinitions()
	if err != nil {
		return err
	}
	return externalCrewNativeDefinition(add, "crew_integrations", "Run this Crew's shared Slack and Google integration tools directly. action=list returns the enabled native names, descriptions and exact argument schemas; otherwise action is that tool name and arguments matches its schema. Requires current editable Crew ownership, crews:write and unrestricted Crew file consent. Existing bot route, Google read/write grants and account roles apply. Shared Google account grant changes additionally require a current administrator and consent covering all Crews. Human Google authorization, infrastructure review and incoming-email sender approval remain in their existing website flows.", tools)
}

func externalCrewNativeDefinition(add func(string, string, bool, bool, map[string]any, ...string), group, description string, tools []llmtypes.Tool) error {
	names, schemas := []any{"list", "history"}, []any{}
	for _, tool := range tools {
		names = append(names, tool.Function.Name)
		schema, err := crewExecutionSchema(tool)
		if err != nil {
			return err
		}
		schemas = append(schemas, schema)
	}
	add(group, description, false, false, map[string]any{
		"crew_id":   externalString("Crew ID from crew list."),
		"action":    map[string]any{"type": "string", "enum": names},
		"arguments": map[string]any{"anyOf": schemas},
	}, "crew_id", "action")
	return nil
}

func (api *StreamingAPI) externalCrewExecutionCall(w http.ResponseWriter, r *http.Request, group string, args map[string]any) {
	claims := GetUserFromContext(r.Context())
	id, action := externalArg(args, "crew_id"), externalArg(args, "action")
	if status, message := externalCrewAuthorGate(claims); status != 0 {
		externalError(w, status, "forbidden", message)
		return
	}
	if token := claims.AccessToken; token != nil && (!token.Allows("crews:write") || !token.AllowsCrew(id)) {
		externalError(w, 403, "insufficient_scope", "This connection cannot execute authoring tools in this Crew.")
		return
	}
	crew, _, _, ok := api.externalCrewResolve(r.Context(), claims, id)
	if !ok {
		externalError(w, 404, "not_found", "Crew not found or not allowed.")
		return
	}
	if !crew.OwnedByCaller {
		externalError(w, 403, "forbidden", "Only the Crew owner can execute these tools.")
		return
	}
	if action == "history" {
		if args["arguments"] != nil {
			externalError(w, 400, "invalid_arguments", "history takes no arguments.")
			return
		}
		rows, err := crewExecutionHistory(r.Context(), id)
		if err != nil {
			externalFailure(w, err)
			return
		}
		externalJSON(w, map[string]any{"crew_id": id, "calls": rows})
		return
	}
	profile, err := api.agentProfiles.Resolve("work", 0, claims.UserID)
	if err != nil {
		externalFailure(w, err)
		return
	}
	var selected llmtypes.Tool
	inventory := []map[string]any{}
	tools := crewExecutionDefinitions()
	if group == "crew_integrations" {
		tools, err = crewIntegrationDefinitions()
		if err != nil {
			externalFailure(w, err)
			return
		}
	}
	for _, tool := range tools {
		member := tool.Function.Name
		if member == "call_selected_mcp_tool" {
			member = "trigger_mcp_discovery"
		}
		if !containsExactString(profile.ToolPolicy.Enabled, member) {
			continue
		}
		schema, err := crewExecutionSchema(tool)
		if err != nil {
			externalFailure(w, err)
			return
		}
		inventory = append(inventory, map[string]any{"name": tool.Function.Name, "description": tool.Function.Description, "inputSchema": schema})
		if tool.Function.Name == action {
			selected = tool
		}
	}
	if action == "list" {
		if args["arguments"] != nil {
			externalError(w, 400, "invalid_arguments", "list takes no arguments.")
			return
		}
		externalJSON(w, map[string]any{"crew_id": id, "tools": inventory})
		return
	}
	if selected.Function == nil {
		externalError(w, 404, "tool_unavailable", "This Crew profile does not enable this tool.")
		return
	}
	params, _ := args["arguments"].(map[string]any)
	if params == nil {
		params = map[string]any{}
	}
	schema, err := crewExecutionSchema(selected)
	if err != nil {
		externalFailure(w, err)
		return
	}
	compiler := jsonschema.NewCompiler()
	if err = compiler.AddResource("crew-execution.json", schema); err != nil {
		externalFailure(w, err)
		return
	}
	validator, err := compiler.Compile("crew-execution.json")
	if err != nil {
		externalFailure(w, err)
		return
	}
	if err = validator.Validate(params); err != nil {
		externalError(w, 400, "invalid_arguments", err.Error())
		return
	}
	guard := externalFileGuard(r.Context(), true)
	if action != "execute_shell_command" && guard != nil {
		externalError(w, 403, "insufficient_scope", "Browser, connected MCP, integrations and managed database tools require unrestricted Crew file consent.")
		return
	}
	root := agentProfileRuntimeWorkspace(crew.OwnerID, crew.Binding.WorkspacePath)
	if group == "crew_integrations" && (action == "update_gmail_connection_grants" || action == "setup_gmail_inbound" || action == "manage_gmail_trigger" && externalArg(params, "action") == "connect") {
		if !currentUserIsAdmin(r) || claims.AccessToken != nil && !claims.AccessToken.AllCrews {
			externalError(w, 403, "forbidden", "Shared Google account management requires an administrator and consent covering all Crews.")
			return
		}
	}
	connection := "interactive"
	if claims.AccessToken != nil {
		connection = claims.AccessToken.ID
	}
	digest := sha256.Sum256([]byte(claims.UserID + "\x00" + connection + "\x00" + id))
	session := "external-crew-" + hex.EncodeToString(digest[:16])
	release, err := lockCrewExecution(r.Context(), session)
	if err != nil {
		externalFailure(w, err)
		return
	}
	defer release()
	defer common.ClearSessionShellConfig(session)
	reads, writes, blocked, blockedWrites := crewExecutionFilePaths(root, guard)
	common.SetSessionWorkingDir(session, root)
	common.SetSessionFolderGuard(session, reads, writes)
	common.SetSessionFolderGuardBlockedPaths(session, blocked)
	common.SetSessionFolderGuardBlockedWritePaths(session, blockedWrites)
	common.SetSessionSandbox(session, profile.Runtime.Sandbox.IsStrict(), profile.Runtime.Sandbox.NetworkDisabled())
	bindConversationBrowserIsolation(session, claims.UserID, root, nil)
	// Only trusted transport code assigns database authority and location.
	common.SetSessionShellEnv(session, map[string]string{"DB_PATH": path.Join(root, "db", "db.sqlite"), "WORKFLOW_DB_ACCESS": "read-write"})
	ctx := context.WithValue(r.Context(), common.ChatSessionIDKey, session)
	ctx = context.WithValue(ctx, common.WorkflowSessionIDKey, session)
	ctx = context.WithValue(ctx, common.UserIDKey, claims.UserID)
	ctx = context.WithValue(ctx, externalCrewMCPPlaceKey{}, root)
	ctx = context.WithValue(ctx, externalCrewDirectAuthorityKey{}, externalCrewDirectAuthority{crewID: id, root: root, user: claims.UserID})
	client := workspace.NewClient(getWorkspaceAPIURL(), workspace.WithUserID(claims.UserID), workspace.WithDefaultWorkingDir(root))
	executors := workspace.NewAdvancedExecutor(client)
	if group == "crew_integrations" {
		reg, err := api.crewIntegrationRegistry(root, session)
		if err != nil {
			externalFailure(w, err)
			return
		}
		tool, found := reg[action]
		if !found || !crewIntegrationMembers[action] {
			externalError(w, 404, "tool_unavailable", "Integration tool unavailable.")
			return
		}
		executors[action] = tool.execute
	} else if action == "execute_shell_command" {
		if params["timeout"] == nil {
			params["timeout"] = float64(25)
		}
	} else if action == "agent_browser" {
		settings, err := readWorkspaceBrowserSettings(ctx, root)
		if err != nil {
			externalError(w, 502, "browser_settings_unavailable", "Cannot read this Crew's browser settings.")
			return
		}
		common.SetSessionBrowserMode(session, settings.Mode)
		if err = registerCodingBrowserTools(crewCapabilityRegistrarBrowser(executors), session, settings.Mode, []int{settings.Port}, nil); err != nil {
			externalFailure(w, err)
			return
		}
	} else if action == "call_selected_mcp_tool" {
		executors[action] = func(ctx context.Context, params map[string]any) (string, error) {
			return api.executeCrewSelectedMCP(ctx, root, params)
		}
	} else {
		executors = virtualtools.CreateWorkflowDBToolRegistry(getWorkspaceAPIURL(), claims.UserID, session).Executors
	}
	if common.GetSessionShellConfig(session) == nil {
		externalError(w, 503, "execution_context_unavailable", "Trusted execution context expired before tool dispatch.")
		return
	}
	audit, auditID, err := startCrewExecutionAudit(ctx, claims.UserID, id, connection, group, action, params)
	if err != nil {
		externalError(w, 503, "audit_unavailable", "Execution was not started because its durable audit record could not be saved.")
		return
	}
	defer audit.Close()
	result, err := executors[action](ctx, params)
	if auditErr := finishCrewExecutionAudit(audit, auditID, result, err); auditErr != nil {
		externalError(w, 503, "audit_completion_unavailable", "Execution may have completed; inspect history before retrying. Audit ID: "+auditID)
		return
	}
	if err != nil {
		externalError(w, 400, "execution_rejected", err.Error())
		return
	}
	var value any
	if json.Unmarshal([]byte(result), &value) != nil {
		value = result
	}
	externalJSON(w, map[string]any{"crew_id": id, "action": action, "result": value})
}

func (api *StreamingAPI) executeCrewSelectedMCP(ctx context.Context, root string, params map[string]any) (string, error) {
	// Use the shared bridge execution path, including connection refresh,
	// upstream errors and images. Its resolver must always decide; nil may not
	// fall through to process-wide or another session's connection registries.
	handler := executor.NewExecutorHandlers(api.mcpConfigPath, api.logger)
	handler.SetMCPServerResolver(func(callCtx context.Context, _ string, server, tool string) (*executor.ResolvedMCPServer, error) {
		callCtx = context.WithValue(callCtx, placeScopedKey{}, true)
		if resolved, handled, err := api.resolvePlaceAttachedMCP(callCtx, "", server); handled {
			return resolved, err
		}
		manifest, found, err := ReadWorkflowManifest(callCtx, root)
		if err != nil || !found {
			return nil, fmt.Errorf("Crew MCP selection is unavailable")
		}
		catalog, err := mcpclient.LoadMergedConfig(api.mcpConfigPath, api.logger)
		if err != nil {
			return nil, err
		}
		resolved, err := api.resolveScopedGovernedMCP(callCtx, catalog, runtimeMCPServers(manifest.Capabilities.SelectedServers), manifest.Capabilities.SelectedTools, GetUserIDFromContext(callCtx), server, tool)
		if err != nil {
			return nil, err
		}
		if resolved == nil {
			return nil, fmt.Errorf("MCP server is not available in this Crew")
		}
		return resolved, nil
	})
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	data, err := json.Marshal(map[string]any{"server": params["server"], "tool": params["tool"], "args": params["args"]})
	if err != nil {
		return "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "/mcp/execute", bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	response := httptest.NewRecorder()
	handler.HandleMCPExecute(response, request)
	var result executor.MCPExecuteResponse
	if err = json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		return "", err
	}
	if !result.Success {
		return "", fmt.Errorf("MCP call refused: %s", result.Error)
	}
	encoded, err := json.Marshal(map[string]any{"result": result.Result, "images": result.Images})
	return string(encoded), err
}

type crewCapabilityRegistrarBrowser map[string]func(context.Context, map[string]any) (string, error)

func (r crewCapabilityRegistrarBrowser) RegisterCustomTool(name, _ string, _ map[string]any, execute func(context.Context, map[string]any) (string, error), _ string) error {
	r[name] = execute
	return nil
}

func crewExecutionFilePaths(root string, guard *wf.FolderGuard) (reads, writes, blocked, blockedWrites []string) {
	join := func(paths []string) []string {
		out := []string{}
		for _, p := range paths {
			out = append(out, path.Join(root, p))
		}
		return out
	}
	reads, writes = []string{root}, []string{root}
	if guard != nil {
		reads = join(append(append(append([]string{}, guard.ReadPaths...), guard.ReadOnlyPaths...), guard.WritePaths...))
		writes = join(guard.WritePaths)
		blocked = join(guard.BlockedPaths)
		blockedWrites = join(append(append([]string{}, guard.BlockedWritePaths...), guard.ReadOnlyPaths...))
	}
	// Shared shell sandbox policy protects coding-agent projections and the
	// same root metadata already protected by external file authoring.
	blockedWrites = append(blockedWrites, common.CodingAgentProjectionBlockedWrites(root)...)
	for _, p := range []string{"workflow.json", "product.json", "functions.json", "schedule-runs.json", "builder", "chat_history", "costs", "human_inputs", "runs", "planning", "db/db.sqlite", "db/db.sqlite-wal", "db/db.sqlite-shm"} {
		blockedWrites = append(blockedWrites, path.Join(root, p))
	}
	return
}
