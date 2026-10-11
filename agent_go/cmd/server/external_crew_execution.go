package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"path"
	"sync"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

var crewExecutionMembers = map[string]bool{
	"execute_shell_command": true, "agent_browser": true,
	"mutate_workflow_db": true, "apply_workflow_db_migration": true, "create_workflow_database_snapshot": true,
}

// Fixed lock striping bounds memory and prevents two calls from replacing the
// same connection's temporary policy. These sessions never adopt a chat.
var crewExecutionLocks [64]sync.Mutex

func crewExecutionDefinitions() []llmtypes.Tool {
	tools := append(workspace.GetAdvancedToolDefinitions(), virtualtools.CreateWorkspaceBrowserTools()...)
	tools = append(tools, virtualtools.CreateWorkflowDBToolRegistry("", "", "").Tools...)
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
	names, schemas := []any{"list"}, []any{}
	for _, tool := range crewExecutionDefinitions() {
		names = append(names, tool.Function.Name)
		schema, err := crewExecutionSchema(tool)
		if err != nil {
			return err
		}
		schemas = append(schemas, schema)
	}
	add("crew_execution", "Execute a shared Crew shell, browser or managed database tool directly, without a model turn. action=list returns the enabled tools' descriptions and exact argument schemas; otherwise action is that tool name and arguments matches its schema. Requires crews:write, current editable account and Crew ownership. Uses this Crew's sandbox and connection file limits; never borrows a chat's authority. Shell timeout defaults to 25 seconds. Browser and database operations require unrestricted Crew file consent; credentials remain in Integrations. Database reads use query_database.", false, false, map[string]any{
		"crew_id":   externalString("Crew ID from crew list."),
		"action":    map[string]any{"type": "string", "enum": names},
		"arguments": map[string]any{"anyOf": schemas},
	}, "crew_id", "action")
	return nil
}

func (api *StreamingAPI) externalCrewExecutionCall(w http.ResponseWriter, r *http.Request, args map[string]any) {
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
	profile, err := api.agentProfiles.Resolve("work", 0, claims.UserID)
	if err != nil {
		externalFailure(w, err)
		return
	}
	var selected llmtypes.Tool
	inventory := []map[string]any{}
	for _, tool := range crewExecutionDefinitions() {
		if !containsExactString(profile.ToolPolicy.Enabled, tool.Function.Name) {
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
		externalError(w, 403, "insufficient_scope", "Browser and managed database tools require unrestricted Crew file consent.")
		return
	}
	root := agentProfileRuntimeWorkspace(crew.OwnerID, crew.Binding.WorkspacePath)
	connection := "interactive"
	if claims.AccessToken != nil {
		connection = claims.AccessToken.ID
	}
	digest := sha256.Sum256([]byte(claims.UserID + "\x00" + connection + "\x00" + id))
	session := "external-crew-" + hex.EncodeToString(digest[:16])
	lock := &crewExecutionLocks[int(digest[0])%len(crewExecutionLocks)]
	lock.Lock()
	defer lock.Unlock()
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
	client := workspace.NewClient(getWorkspaceAPIURL(), workspace.WithUserID(claims.UserID), workspace.WithDefaultWorkingDir(root))
	executors := workspace.NewAdvancedExecutor(client)
	if action == "execute_shell_command" {
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
	} else {
		executors = virtualtools.CreateWorkflowDBToolRegistry(getWorkspaceAPIURL(), claims.UserID, session).Executors
	}
	result, err := executors[action](ctx, params)
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
