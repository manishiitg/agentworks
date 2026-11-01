package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/knowledgebase"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productpolicy"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// externalMCPPath is the MCP Streamable HTTP endpoint. It exposes the same
// external tool catalog, schemas, scopes, and PAT auth as the REST external
// API for hosted MCP clients (ChatGPT, Claude Cowork) that cannot spawn the
// local stdio bridge.
//
// Unlike the CLI and stdio bridge, which list every tool, the remote surface
// uses three self-describing tools: search_tools finds names by keyword,
// get_api_spec returns schemas, call_tool executes. The full catalog (product.yaml's external_tools plus
// run.tools) is resolved internally, so the MCP surface stays tiny no matter
// how run mode grows, and hosted clients without tool search never face a
// truncated tail. The dashboard render tool and its app-only data reader add
// the MCP Apps resource contract for authorized Crew connections.
const externalMCPPath = "/api/external/v1/mcp"

const (
	externalMCPToolSpec = "get_api_spec"
	externalMCPToolCall = "call_tool"
)

// externalMCPToolNames is the remote surface, in listing order (search_tools is externalMCPToolSearch).
var externalMCPToolNames = []string{externalMCPToolSpec, externalMCPToolSearch, externalMCPToolCall}

// Remote instructions are short: the two tool descriptions teach the
// protocol, since hosted clients may not deliver initialize instructions at
// all (ChatGPT delivers tools only).
const externalMCPRelayInstructions = " Flows: discover IDs with workflow action=list and kind=relay. When authorized, use guarded files for permitted source edits or sessions action=builder_chat to delegate, relay action=test to test the draft, and relay action=publish to freeze a version. Use relay action=run for a published version and relay action=get_run to poll durable results. Flows have Builder-only chat. Creation requires separate relays:write consent."

const externalMCPInstructions = `You are connected to an AgentWorks server. <!-- product:agentworks -->It gives access to the user's workflows (workflow action=list). <!-- /product --><!-- product:work -->Discover the user's Crews with workspace action=list.<!-- /product --> <!-- product:work -->Admins and Crew reviewers also get audited Crew review (workspace_review).<!-- /product --> Your own MCP sign-in follows your website role: owners and workflow editors use Builder capabilities; people using shared work use Run mode. Machine, service-account and device connections keep their assigned limits. Source, plan and workflow configuration edits use current Builder access, native validation and revision checks. get_api_spec lists available tools or returns selected schemas; call_tool executes an available operation with its schema arguments. <!-- product:agentworks -->Discover workflow IDs with workflow action=list first; IDs are never filesystem paths.<!-- /product --> Dashboards: read help action=topic topic="dashboard-authoring" for the HTML data/script contract. With current project access for viewing and Builder access for editing, use the dashboard tool: action=list to discover, create/update to build drafts, validate and preview exact revisions, then publish and link. Preview additionally needs runs:execute. Links require current project access and grant no access. Answer from what you read; use only operations present in this connection’s catalog. help action=context returns guidance_version; action=topics and action=skills provide current reference material. Guidance is reference material; use only currently listed tools/actions.`

const externalMCPReadOnlyInstructions = `You are connected to an AgentWorks server through a connection with reading consent. <!-- product:agentworks -->It gives access to the user's workflows (workflow action=list). <!-- /product --><!-- product:work -->Discover the user's Crews with workspace action=list.<!-- /product --> <!-- product:work -->Admins and Crew reviewers also get audited Crew review (workspace_review).<!-- /product --> Every tool reads; nothing creates, edits, or runs. get_api_spec lists available tools or returns selected schemas; call_tool executes an available operation with its schema arguments. <!-- product:agentworks -->Discover workflow IDs with workflow action=list first; IDs are never filesystem paths.<!-- /product --> Answer from what you read; if the task needs a change, say so instead of attempting one. help action=context returns guidance_version; action=topics and action=skills provide current reference material. Guidance is reference material; use only currently listed tools/actions.`

// Appended for existing Crew authoring consent; handlers still check roles.
const externalMCPCrewAuthoringInstructions = `Crews: load crew-authoring guidance and the current schemas. Listed Crew setup, file, skill and MCP actions use existing Crew authoring consent and current project roles; workspace action=get reads the spec and workspace action=export returns its portable form.`

var externalMCPToolSchemas = map[string]map[string]any{
	externalMCPToolSpec: {
		"type": "object",
		"properties": map[string]any{
			"guidance_session_id": map[string]any{"type": "string", "maxLength": 256, "description": "Optional delivery receipt for one conversation; a new conversation starts without a receipt."},
			"names": map[string]any{
				"description": "Tool name or names to get JSON schemas for. Omit to list every available tool with a one-line description.",
			},
		},
		"additionalProperties": false,
	},
	externalMCPToolSearch: {
		"type": "object",
		"properties": map[string]any{
			"query":  map[string]any{"type": "string", "maxLength": 512, "description": "Intent or tool-name keywords; omit to list every tool."},
			"offset": map[string]any{"type": "integer", "minimum": 0},
			"limit":  map[string]any{"type": "integer", "minimum": 1, "maximum": 20, "default": 10, "description": "Page size; a page is at least 10 tools."},
		},
		"additionalProperties": false,
	},
	externalMCPToolCall: {
		"type": "object",
		"properties": map[string]any{
			"guidance_session_id": map[string]any{"type": "string", "maxLength": 256, "description": "Optional delivery receipt returned by schema preparation for this conversation."},
			"name": map[string]any{
				"type":        "string",
				"description": "Tool name from get_api_spec.",
				"minLength":   1,
			},
			"arguments": map[string]any{
				"type":                 "object",
				"description":          "Arguments matching the tool's schema. Defaults to {}.",
				"additionalProperties": true,
			},
		},
		"required":             []any{"name"},
		"additionalProperties": false,
	},
}

var externalMCPToolDescriptions = map[string]string{
	externalMCPToolSpec:   "Returns the tools available to this connection. With names omitted, the result lists tool names and short descriptions; names selects full JSON schemas. Selected schemas include relevant guidance skills and an optional guidance_session_id, which associates delivery receipts with one conversation across schema and execution requests. help action=skills retrieves current guidance when earlier content is unavailable. This lookup does not execute a tool or change project data.",
	externalMCPToolSearch: "Searches available AgentWorks tools by intent or name. Returns tool names and short descriptions, ten per page, with next_offset when more results exist. Full schemas are available through get_api_spec; execution uses call_tool. Search does not change access or project data.",
	externalMCPToolCall:   "Executes one available AgentWorks tool by name with arguments matching its JSON schema. get_api_spec supplies schemas and workflow action=list supplies workflow IDs. The selected operation runs with the connection's existing permissions and current project access.",
}

// handleExternalMCP serves the remote tools and app resources over MCP Streamable
// HTTP. It runs stateless: every request is independently authenticated,
// matching the per-request PAT validation of the REST API.
func (api *StreamingAPI) handleExternalMCP(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	if claims == nil {
		externalError(w, http.StatusUnauthorized, "unauthorized", "Sign in to AgentWorks.")
		return
	}
	// Header-less MCP clients pass the token in ?token=; never let responses cache.
	w.Header().Set("Cache-Control", "no-store")
	if api.externalMCPTaskHTTP(w, r) {
		return
	}
	catalog, err := externalTools()
	if err != nil {
		externalError(w, http.StatusInternalServerError, "schema_error", err.Error())
		return
	}
	allowed := make([]externalTool, 0, len(catalog))
	for _, tool := range catalog {
		if externalTokenAllows(claims, tool) {
			allowed = append(allowed, externalMessageToolForRequest(r, knowledgebaseToolForClaims(claims, tool)))
		}
	}
	instructions := externalMCPReadOnlyInstructions
	for _, tool := range allowed {
		// The catalog omits run tools from tokens lacking runs:execute, so
		// execute_step's presence proves this connection runs.
		if tool.Name == "create_dashboard" || tool.Name == "write_file" || tool.Name == "execute_step" || tool.Name == knowledgebase.ToolUpdate || isExternalVaultTool(tool.Name) || tool.Name == serverProductManagementTool {
			instructions = externalMCPInstructions
			break
		}
	}
	for _, tool := range allowed {
		if tool.Name == serverProductManagementTool {
			instructions += " Installed private server products: server_product action=tools discovers their management schemas; action=call invokes them with current admin authority and users:manage consent. Inference keys do not authorize management. "
		}
		if tool.Name == "brain" {
			instructions += " Brain stores shared files of any type. Use brain actions for folders, entries, read/search, changes/diff and saves; skills_list/skills_get/skills_publish manage company skills, and access_* actions use current folder ownership. Install a skill by writing its returned files under your project's skills folder. The catalog reflects this connection's actions. Updates/deletes require expected_version and a stable request_id. Access changes require current expected_acl_version and request_id. Every save is a Git commit; configured backup runs automatically after saves. App access-chat changes retain app confirmation."
			break
		}
	}
	for _, tool := range allowed {
		if tool.Name == "brain" && tool.actions["create"] != "" {
			instructions += " This connection also authorizes Brain saves. Saves affect only folders where its identity currently has Editor access, intersected with its folder caps; workflow authoring permissions are separate."
			break
		}
	}
	for _, tool := range allowed {
		if tool.Name == "vault" && tool.actions["access_save_permissions"] != "" {
			instructions += " Vault actions use current account, project and administrator access. Use the offered access_*, groups_* and tools_* actions for administration, governance_query/governance_mutate for guarded SQL, and connections/call for approved setup calls. set_value stores a secret the person supplies and returns names only; never repeat values. Project secrets also use settings action=update secrets.set with existing owner access. Vault administration needs no workflow_id; ordinary runtime calls retain group and selection checks."
		}
	}
	for _, tool := range allowed {
		if tool.Name == "get_token_usage" {
			instructions += " Shared-account token limits: account action=usage shows each person's tokens on the server's shared accounts today and this week (UTC, Monday weeks) against their daily/weekly limits, or totals for a from/to range; action=set_limits (administrators, users:manage) changes a person's limits (0 or null = unlimited, omitted = unchanged; in a person's account override 0 falls back to the account default and -1 is unlimited even when the account has a default). Allowed models: action=usage also lists each shared account's models (account_allowed_models) and each person's effective models per account (allowed_models); action=set_allowed_models (administrators, users:manage) sets an account's list (null = all models) or, with a person, their override (a list replaces the account's; null clears it; [\"*\"] or all_models = every model). Every call is recorded in the Code review audit log."
			break
		}
	}
	for _, tool := range allowed {
		if tool.Name == externalUsersTool {
			instructions += " Administrator accounts: People & access is an overview with basic role, product and on/off updates. Use users list/get/update/delete for detailed admin changes; its schema defines request_id and existing confirmation rules. Passwords must be person-supplied and never repeated or logged. Use account for usage, limits and model rules. Account provisioning remains separate."
			break
		}
	}
	for _, tool := range allowed {
		if isExternalWorkflowAccountTool(tool.Name) {
			instructions += " Workflow and Flow personal accounts: settings action=google_account or slack_account takes workflow_id and account containing the native account action. Start with account.action=list_saved, then attach/detach a saved account here; connect/reconnect returns a browser sign-in link. Existing Builder consent and account ownership apply. detach affects this item only; disconnect removes the saved connection everywhere. Browser sign-in remains a human action."
			break
		}
	}
	for _, tool := range allowed {
		if tool.Name == "create_crew" {
			instructions += " " + externalMCPCrewAuthoringInstructions
			break
		}
	}
	for _, tool := range allowed {
		if tool.Name == "plan" {
			instructions += " Direct plans: plan action=list returns native schemas. Read workflow action=plan for expected_revision, then edit with the exact arguments and a unique request_id; identical retries return the receipt. Current roles, Builder consent, native validators and plan changelog apply."
		}
		if tool.Name == "workflow_upgrade" {
			instructions += " Contract upgrades: workflow_upgrade action=status only lists owed migrations. Begin an explicit upgrade with action=prepare before Builder chat or plan checkers; this converts retired agent records without stamping. Do each migration's work in order, then action=stamp with version and confirm=workflow_id. The Builder's stamp checks and ordering apply."
		}
		if tool.Name == "crew_execution" {
			instructions += " Direct Crew tools: terminal, browser, database, mcp and integrations run with crew_id; authorized owners execute them without a model turn. History is redacted and owner-only. sessions action=runtime_tools/runtime_call address only your own chat; background starts require its initialized native sandbox and UI actions return actual browser receipts. Human sign-in and approval retain app flows."
		}
	}
	for _, tool := range allowed {
		if tool.Name == "workspace" {
			instructions += " Project creation: use workspace action=create with kind=goal|crew|relay, spec matching that kind and a stable idempotency_key. Goal spec needs name and optional purpose; the server supplies its ID, current defaults and an empty plan. Optional folder_name, workflow_json and plan_json provide initial authoring details. Crew spec contains name and purpose. Flow spec contains label. Account creation rights and unrestricted Builder consent are required. The new workflow belongs to this user; use its returned workflow_id for direct plan edits, guarded files and authorized KB project bindings. Creation does not run it."
		}
		if tool.Name == "run_relay" {
			instructions += externalMCPRelayInstructions
		}
		if tool.Name == "builder_chat" {
			instructions += " Builder: use sessions action=builder_chat to send editing requests to the configured model in your existing workflow chat. Use a unique submission_id, poll sessions action=builder_status by operation_id, and answer pending questions with sessions action=builder_reply_input. sessions action=builder_cancel stops only that operation. Check help action=context with workflow_id for current effective tools."
			break
		}
	}
	instructions = (productpolicy.Selection{}).Text(instructions)
	index, fingerprint := externalMCPToolIndex(externalListedTools(claims, allowed))
	serviceName := "AgentWorks"
	mcpServer := server.NewMCPServer(serviceName, "1.0.0",
		server.WithToolCapabilities(false),
		server.WithResourceCapabilities(false, false),
		server.WithElicitation(),
		server.WithExtensions(map[string]any{agentworksclient.MCPTasksExtension: map[string]any{}}),
		server.WithInstructions(instructions),
	)
	api.registerExternalDashboardApp(mcpServer, r)
	for _, name := range externalMCPToolNames {
		schema, err := json.Marshal(externalMCPToolSchemas[name])
		if err != nil {
			externalError(w, http.StatusInternalServerError, "schema_error", err.Error())
			return
		}
		mcpServer.AddTool(
			mcp.NewToolWithRawSchema(name, strings.ReplaceAll(externalMCPToolDescriptionFor(name, index), "AgentWorks", serviceName), schema),
			func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return api.externalMCPCall(ctx, r, name, allowed, request), nil
			},
		)
	}
	httpServer := server.NewStreamableHTTPServer(mcpServer,
		// No session state is kept: the session ID is this connection's tool
		// fingerprint, so a client whose tools changed (a deploy, a role
		// change) gets 404 and reconnects, which re-reads the tool menu.
		server.WithSessionIdManager(externalMCPCatalogSession{fingerprint: fingerprint}),
		// This is an authenticated deployment behind Caddy, not a local
		// browser-driven server: the loopback listener plus public Host
		// header would trip the DNS-rebinding guard on every proxied request.
		server.WithDisableLocalhostProtection(true),
	)
	httpServer.ServeHTTP(w, r)
}

// externalMCPCall dispatches one remote tool call: get_api_spec resolves
// against the scope-filtered catalog, call_tool runs through the REST
// external dispatcher so results can never drift from the REST surface.
func (api *StreamingAPI) externalMCPCall(ctx context.Context, r *http.Request, name string, allowed []externalTool, request mcp.CallToolRequest) *mcp.CallToolResult {
	// Temporary observation for PLAT-856: compare ChatGPT conversation metadata
	// across schema reads and calls. Never log the value, other metadata,
	// headers or input.
	if (name == externalMCPToolSpec || name == externalMCPToolCall) && request.Params.Meta != nil {
		fields := request.Params.Meta.AdditionalFields
		value, present := fields["openai/session"]
		userAgent, _ := fields["openai/userAgent"].(string)
		if present || strings.Contains(strings.ToLower(userAgent), "chatgpt") {
			session, isString := value.(string)
			hash := "absent"
			if isString && session != "" {
				sum := sha256.Sum256([]byte(session))
				hash = hex.EncodeToString(sum[:8])
			}
			logfWithContext(api.httpRequestLogContext(r), "[EXTERNAL_MCP_SESSION_PROBE] session_present=%t session_string=%t session_hash=%s", present, isString, hash)
		}
	}
	result := api.externalMCPDispatch(ctx, r, name, allowed, request)
	logfWithContext(api.httpRequestLogContext(r), "%s", externalMCPCallLogLine(name, request.GetArguments(), result))
	return result
}

// externalMCPCallLogLine is the one log line of an outside MCP tool call: the
// tool, the outcome and, when it failed, the error code and message. Never the
// tool arguments or content (a refused secret write must not leave its value
// in the log). Workspace tools additionally name only their UUID selectors.
func externalMCPCallLogLine(name string, args map[string]any, result *mcp.CallToolResult) string {
	target := name
	action := ""
	selectors := args
	if name == externalMCPToolCall {
		target, _ = args["name"].(string)
		if inner, ok := args["arguments"].(map[string]any); ok {
			action, _ = inner["action"].(string)
			selectors = inner
		}
	}
	clip := func(value string, limit int) string {
		value = strings.Join(strings.Fields(value), " ")
		if len(value) > limit {
			value = value[:limit] + "…"
		}
		return value
	}
	line := fmt.Sprintf("[EXTERNAL_MCP] call=%s tool=%s", name, clip(target, 80))
	if action != "" {
		line += " action=" + clip(action, 40)
	}
	line += externalMCPWorkspaceSelectors(target, selectors)
	if result == nil || !result.IsError {
		return line + " outcome=ok"
	}
	text := ""
	for _, content := range result.Content {
		if part, ok := content.(mcp.TextContent); ok {
			text = part.Text
			break
		}
	}
	code, message := "error", text
	if i := strings.Index(text, ": "); i > 0 && i <= 40 && !strings.ContainsAny(text[:i], " \t") {
		code, message = text[:i], text[i+2:]
	}
	return fmt.Sprintf("%s outcome=refused code=%s error=%q", line, clip(code, 40), clip(message, 240))
}

// Log requested project/agent identities even when access was revoked before
// resolution. Only UUID identifiers are accepted here, never paths, message
// text, file contents or arbitrary argument values. The authenticated request
// log context supplies the caller; these selectors do not claim admission.
func externalMCPWorkspaceSelectors(tool string, args map[string]any) string {
	switch tool {
	case "workspace", "files", "share", "dashboard", "read_crew_file", "list_crew_files", "search_crew_files", "write_crew_file", "share_dashboard", "get_dashboard_share":
	default:
		return ""
	}
	workspace := externalArg(args, "workspace_id")
	if workspace == "" {
		workspace = externalArg(args, "crew_id")
		if workspace != "" && !strings.Contains(workspace, ":") {
			workspace = "work:" + workspace
		}
	}
	if workspace == "" {
		workspace = externalArg(args, "workspace")
	}
	origin, id, ok := strings.Cut(workspace, ":")
	if !ok || origin != "work" {
		return ""
	}
	parsed, err := uuid.Parse(id)
	if err != nil {
		return ""
	}
	fields := fmt.Sprintf(" workspace_id=%q", origin+":"+parsed.String())
	if agent := externalArg(args, "agent_id"); agent == "main" {
		fields += ` agent_id="main"`
	} else if parsed, err := uuid.Parse(agent); err == nil {
		fields += fmt.Sprintf(" agent_id=%q", parsed.String())
	}
	return fields
}

func (api *StreamingAPI) externalMCPDispatch(ctx context.Context, r *http.Request, name string, allowed []externalTool, request mcp.CallToolRequest) *mcp.CallToolResult {
	args := request.GetArguments()
	if args == nil {
		args = map[string]any{}
	}
	if name == externalMCPToolSearch {
		return externalMCPSearchTools(args, externalListedTools(GetUserFromContext(r.Context()), allowed))
	}
	if name == externalMCPToolSpec {
		return externalMCPAPISpec(args, externalListedTools(GetUserFromContext(r.Context()), allowed), func(value map[string]any, names []string) {
			handle, _ := args["guidance_session_id"].(string)
			api.externalPrepareGuidance(r, value, handle)
		})
	}
	handle, _ := args["guidance_session_id"].(string)
	r = externalWithGuidanceHandle(r.WithContext(ctx), handle)
	ctx = r.Context()
	target, _ := args["name"].(string)
	target = strings.TrimSpace(target)
	if target == "" {
		return mcp.NewToolResultError("invalid_arguments: call_tool requires the tool name in \"name\" (discover names with get_api_spec)")
	}
	byName := make(map[string]externalTool, len(allowed))
	for _, tool := range allowed {
		byName[tool.Name] = tool
	}
	if tool, ok := byName[target]; ok && tool.internalOnly {
		return mcp.NewToolResultError("unknown_tool: refresh the tool catalog with get_api_spec and choose its current action tool")
	}
	if _, ok := byName[target]; !ok {
		catalog, err := externalTools()
		if err == nil {
			for _, tool := range catalog {
				if tool.Name == target {
					return mcp.NewToolResultError("insufficient_scope: this connection may not call \"" + target + "\"." + externalMissingScopeHint(GetUserFromContext(r.Context()), tool))
				}
			}
		}
		return mcp.NewToolResultError("unknown_tool: \"" + target + "\" is not exposed by this API; call get_api_spec with no arguments for the available tools")
	}
	callArgs, _ := args["arguments"].(map[string]any)
	if callArgs == nil {
		callArgs = map[string]any{}
	}
	// A merged tool runs as its underlying tool from here on, so the function-call
	// question flow below sees the tool it knows.
	translated := false
	if merged := byName[target]; merged.actions != nil {
		translated = true
		member, rest, err := externalResolveMerged(merged, callArgs)
		if err != nil {
			return mcp.NewToolResultError("invalid_arguments: " + err.Error())
		}
		target, callArgs = member.Name, rest
	}
	// A function call with a pending question may come back as an MCP form
	// for clients that declare elicitation (docs/design/mcp_elicitation.md).
	serviceTool, serviceArgs := agentSourceCall(externalTool{Name: target}, callArgs)
	if result := api.externalMCPURLCall(r.WithContext(ctx), serviceTool.Name, serviceArgs, request); result != nil {
		return result
	}
	elicitation := api.externalMCPElicitation(r, byName)
	if request.Params.RequestState != "" {
		return elicitation.Resume(ctx, target, callArgs, request)
	}
	if len(request.Params.InputResponses) > 0 {
		return mcp.NewToolResultError("invalid_request_state: input responses need the matching requestState")
	}
	rec, err := api.externalMCPInvoke(ctx, r, target, callArgs, translated)
	if err != nil {
		return mcp.NewToolResultError("failed to encode tool arguments: " + err.Error())
	}
	if rec.status >= 200 && rec.status < 300 {
		if result := elicitation.MaybeElicit(ctx, target, callArgs, rec.body.Bytes()); result != nil {
			return result
		}
	}
	if target == "preview_dashboard" && rec.status >= 200 && rec.status < 300 {
		return api.externalMCPDashboardPreviewResult(r, rec)
	}
	return externalMCPDispatchResult(rec)
}

func (api *StreamingAPI) externalMCPInvoke(ctx context.Context, r *http.Request, target string, callArgs map[string]any, translated ...bool) (*externalMCPRecorder, error) {
	body, err := json.Marshal(map[string]any{"name": target, "arguments": callArgs})
	if err != nil {
		return nil, err
	}
	sub := r.Clone(ctx)
	sub.Body = io.NopCloser(bytes.NewReader(body))
	sub.ContentLength = int64(len(body))
	rec := &externalMCPRecorder{header: http.Header{}}
	api.dispatchExternalCall(rec, sub, len(translated) > 0 && translated[0])
	return rec, nil
}

func externalMCPDispatchResult(rec *externalMCPRecorder) *mcp.CallToolResult {
	if rec.status < 200 || rec.status >= 300 {
		return mcp.NewToolResultError(externalMCPErrorText(rec))
	}
	var value any
	if err := json.Unmarshal(rec.body.Bytes(), &value); err != nil {
		return mcp.NewToolResultText(rec.body.String())
	}
	result, err := mcp.NewToolResultJSON(value)
	if err != nil {
		return mcp.NewToolResultText(rec.body.String())
	}
	return result
}

// externalMCPAPISpec serves the scope-filtered catalog: no names returns the
// name/description list, names returns full schemas for those tools.
// listed is what the connection is shown; old member names are not readable.
func externalMCPAPISpec(args map[string]any, listed []externalTool, prepare ...func(map[string]any, []string)) *mcp.CallToolResult {
	byName := make(map[string]externalTool, len(listed))
	for _, tool := range listed {
		byName[tool.Name] = tool
	}
	raw, hasNames := args["names"]
	if !hasNames || raw == nil {
		entries := make([]map[string]string, 0, len(listed))
		for _, tool := range listed {
			entries = append(entries, map[string]string{"name": tool.Name, "description": tool.Description})
		}
		result, err := mcp.NewToolResultJSON(map[string]any{"tools": entries, "count": len(entries)})
		if err != nil {
			return mcp.NewToolResultError("failed to encode tool list: " + err.Error())
		}
		return result
	}
	var names []string
	switch typed := raw.(type) {
	case string:
		names = []string{typed}
	case []any:
		for _, item := range typed {
			name, _ := item.(string)
			names = append(names, name)
		}
	case []string:
		names = typed
	default:
		return mcp.NewToolResultError("invalid_arguments: \"names\" must be a string or an array of strings")
	}
	schemas := make(map[string]any, len(names))
	for _, name := range names {
		tool, ok := byName[strings.TrimSpace(name)]
		if !ok {
			return mcp.NewToolResultError("unknown_tool: \"" + name + "\" is not available to this connection; call get_api_spec with no arguments for the available tools")
		}
		schemas[tool.Name] = map[string]any{"description": tool.Description, "inputSchema": llmtypes.ModelToolSchema(tool.InputSchema)}
	}
	value := map[string]any{"schemas": schemas}
	for _, decorate := range prepare {
		decorate(value, names)
	}
	result, err := mcp.NewToolResultJSON(value)
	if err != nil {
		return mcp.NewToolResultError("failed to encode tool schemas: " + err.Error())
	}
	return result
}

// externalMCPRecorder captures the REST dispatcher's response for conversion
// to an MCP tool result.
type externalMCPRecorder struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func (rec *externalMCPRecorder) Header() http.Header { return rec.header }

func (rec *externalMCPRecorder) Write(b []byte) (int, error) {
	if rec.status == 0 {
		rec.status = http.StatusOK
	}
	return rec.body.Write(b)
}

func (rec *externalMCPRecorder) WriteHeader(status int) {
	if rec.status == 0 {
		rec.status = status
	}
}

// externalMCPErrorText renders a failed REST dispatch as tool error text,
// preserving the machine-readable error code.
func externalMCPErrorText(rec *externalMCPRecorder) string {
	status := rec.status
	if status == 0 {
		status = http.StatusInternalServerError
	}
	var failure struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.body.Bytes(), &failure); err == nil && failure.Error.Code != "" {
		return fmt.Sprintf("%s: %s", failure.Error.Code, failure.Error.Message)
	}
	raw := rec.body.String()
	const maxRaw = 1024
	if len(raw) > maxRaw {
		raw = raw[:maxRaw] + "…"
	}
	return fmt.Sprintf("http_%d: %s", status, raw)
}

// externalMCPToolIndex lists the tool names this connection may use, grouped
// by product, for get_api_spec's description. Clients send tool descriptions
// to the model with every message, so the model sees the current names from
// the menu instead of an old get_api_spec answer in its history. The
// fingerprint changes whenever the names do.
func externalMCPToolIndex(allowed []externalTool) (string, string) {
	order := []string{"Goals", "Builder", "Flows", "Crews", "Dashboards", "Needs you", "Brain", "Vault", "Crew review", "Account", "Help"}
	groups := map[string][]string{}
	names := make([]string, 0, len(allowed))
	for _, tool := range allowed {
		name := tool.Name
		names = append(names, name)
		group := "Goals"
		switch {
		case strings.Contains(name, "dashboard") || name == "get_report_link":
			group = "Dashboards"
		case name == "needs_you" || strings.HasSuffix(name, "_needs_you"):
			group = "Needs you"
		case name == "sessions" || name == "builder" || strings.HasPrefix(name, "builder_"):
			group = "Builder"
		case strings.Contains(name, "relay"):
			group = "Flows"
		case strings.Contains(name, "crew"):
			group = "Crews"
		case name == "brain" || strings.HasPrefix(name, "brain_"):
			group = "Brain"
		case strings.Contains(name, "vault"):
			group = "Vault"
		case name == "workspace_review":
			group = "Crew review"
		case name == "chat_preferences" || name == "users" || name == "costs" || name == "account" || name == "get_token_usage" || name == "set_token_limits" || name == "set_allowed_models" || name == "set_account_availability" || name == "get_account_availability":
			group = "Account"
		case name == "help" || name == "get_agent_context" || name == "get_skill" || strings.Contains(name, "guidance_topic"):
			group = "Help"
		}
		groups[group] = append(groups[group], name)
	}
	var b strings.Builder
	for _, group := range order {
		if len(groups[group]) > 0 {
			b.WriteString("\n" + group + ": " + strings.Join(groups[group], ", "))
		}
	}
	sort.Strings(names)
	sum := sha256.Sum256([]byte("dashboard-app-v1\n" + strings.Join(names, "\n")))
	return b.String(), "aw1-" + hex.EncodeToString(sum[:12])
}

func externalMCPToolDescriptionFor(name, index string) string {
	if name != externalMCPToolSpec || index == "" {
		return externalMCPToolDescriptions[name]
	}
	return externalMCPToolDescriptions[name] + "\n\nTools available to this connection:" + index
}

// externalMCPCatalogSession is a stateless session ID check: the ID is the
// connection's tool fingerprint. Clients on protocol versions with sessions
// send it back; a different fingerprint answers 404, which the MCP standard
// says means start a new session, and a new session re-reads tools/list. A
// client that never received an ID (connected before this check) passes.
type externalMCPCatalogSession struct{ fingerprint string }

func (m externalMCPCatalogSession) Generate() string { return m.fingerprint }

func (m externalMCPCatalogSession) Validate(sessionID string) (bool, error) {
	if sessionID == "" || sessionID == m.fingerprint {
		return false, nil
	}
	return false, errors.New("AgentWorks tools changed; start a new session")
}

func (m externalMCPCatalogSession) Terminate(string) (bool, error) { return false, nil }
