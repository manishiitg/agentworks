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
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// externalMCPPath is the MCP Streamable HTTP endpoint. It exposes the same
// external tool catalog, schemas, scopes, and PAT auth as the REST external
// API for hosted MCP clients (ChatGPT, Claude Cowork) that cannot spawn the
// local stdio bridge.
//
// Unlike the CLI and stdio bridge, which list every tool, the remote surface
// is two self-describing tools: get_api_spec discovers names and schemas,
// call_tool executes. The full catalog (product.yaml's external_tools plus
// run.tools) is resolved internally, so the MCP surface stays tiny no matter
// how run mode grows, and hosted clients without tool search never face a
// truncated tail.
const externalMCPPath = "/api/external/v1/mcp"

const (
	externalMCPToolSpec = "get_api_spec"
	externalMCPToolCall = "call_tool"
)

// Remote instructions are short: the two tool descriptions teach the
// protocol, since hosted clients may not deliver initialize instructions at
// all (ChatGPT delivers tools only).
const externalMCPRelayInstructions = " Relays: discover IDs with list_workflows and kind=relay. When authorized, use builder_chat to edit, test_relay to test the draft, and publish_relay to freeze a version. Use run_relay for a published version and get_relay_run to poll durable results. Relays have Builder-only chat. Creation requires separate relays:write consent."

const externalMCPInstructions = `You are connected to an AgentWorks server. It gives access to the user's workflows (list_workflows) and Crews (list_crews); when asked what is available, cover both. Admins and Code reviewers also get read-only, audited Code workspace review (list_code_workspaces, get_code_costs, list_code_chats, read_code_chat). Tools read, and run-mode tools execute in pinned Run-mode sessions; source and documentation edits require files:write consent and revision checks; plans and workflow configuration require separately granted Builder tools. Call get_api_spec with no arguments to list the available tools, then get_api_spec with names for schemas, then call_tool to execute. Discover workflow IDs with list_workflows first; IDs are never filesystem paths. Dashboards: read get_guidance_topic(topic="dashboard-authoring") for the HTML data/script contract. With explicit dashboards:read/dashboards:write consent, use the dashboard tool: action=list to discover, create/update to build drafts, validate and preview exact revisions, then publish and link. Preview additionally needs runs:execute. Links require current project access and grant no access. Answer from what you read; use only operations present in this connection’s catalog.`

const externalMCPReadOnlyInstructions = `You are connected to an AgentWorks server with a read-only connection. It gives access to the user's workflows (list_workflows) and Crews (list_crews); when asked what is available, cover both. Admins and Code reviewers also get read-only, audited Code workspace review (list_code_workspaces, get_code_costs, list_code_chats, read_code_chat). Every tool reads; nothing creates, edits, or runs. Call get_api_spec with no arguments to list the available tools, then get_api_spec with names for schemas, then call_tool to execute. Discover workflow IDs with list_workflows first; IDs are never filesystem paths. Answer from what you read; if the task needs a change, say so instead of attempting one.`

// Appended when the connection holds crews:write: the one authoring surface
// available to separately authorized Crew connections.
const externalMCPCrewAuthoringInstructions = `Exception: create_crew, update_crew and import_crew create and edit Crews you own (get_crew shows the full spec; export_crew returns a portable spec).`

var externalMCPToolSchemas = map[string]map[string]any{
	externalMCPToolSpec: {
		"type": "object",
		"properties": map[string]any{
			"names": map[string]any{
				"description": "Tool name or names to get JSON schemas for. Omit to list every available tool with a one-line description.",
			},
		},
		"additionalProperties": false,
	},
	externalMCPToolCall: {
		"type": "object",
		"properties": map[string]any{
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
	externalMCPToolSpec: "Discover AgentWorks tools. Call with no arguments to list every available tool name with a one-line description; call with names (a string or an array of strings) to get full JSON schemas for those tools. Only tools this connection may use are listed. Then execute with call_tool.",
	externalMCPToolCall: "Execute one AgentWorks tool by name. Get its schema first with get_api_spec. Arguments must match the tool's schema; workflow tools need the workflow_id from list_workflows.",
}

// handleExternalMCP serves the two-tool remote surface over MCP Streamable
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
	catalog, err := externalTools()
	if err != nil {
		externalError(w, http.StatusInternalServerError, "schema_error", err.Error())
		return
	}
	allowed := make([]externalTool, 0, len(catalog))
	for _, tool := range catalog {
		if externalTokenAllows(claims, tool) {
			allowed = append(allowed, knowledgebaseToolForClaims(claims, tool))
		}
	}
	instructions := externalMCPReadOnlyInstructions
	for _, tool := range allowed {
		// The catalog omits run tools from tokens lacking runs:execute, so
		// execute_step's presence proves this connection runs.
		if tool.Name == "create_dashboard" || tool.Name == "write_file" || tool.Name == "execute_step" || tool.Name == knowledgebase.ToolUpdate || isExternalVaultTool(tool.Name) {
			instructions = externalMCPInstructions
			break
		}
	}
	for _, tool := range allowed {
		if isExternalKnowledgebaseTool(tool.Name) {
			instructions += " Brain stores files of any type (text in content, other files in content_base64) and has five tools with action parameters: brain_browse (folders/entries), brain_read (read/search), brain_update (create/update/delete/create_folder), brain_skills (company skills: list, get to install one by writing its files under your own skills folder, publish a SKILL.md package; scripts need folder Owner), and brain_access (inspect; writable unrestricted external connections may also list, grant/revoke, and manage service accounts and configure backup using remote_url, username and pat_secret (the name of a platform secret holding the token) subject to live Owner/admin authority). The catalog reflects this connection's current read/write actions. Updates/deletes use expected_version and a stable request_id; saves are immediately visible to permitted readers. Every save is a Git commit by the person who made it, and Brain pushes its commits to the configured backup remote automatically a few minutes after the last save; there is no separate backup step. External MCP access changes apply directly after permission checks; grants/revokes require a current expected_acl_version and stable request_id. App access-chat changes still require app confirmation."
			break
		}
	}
	for _, tool := range allowed {
		if tool.Name == knowledgebase.ToolUpdate {
			instructions += " This connection also authorizes Brain saves. Saves affect only folders where its identity currently has Editor access, intersected with its folder caps; workflow authoring permissions are separate."
			break
		}
	}
	for _, tool := range allowed {
		if tool.Name == "manage_vault_access" {
			instructions += " Vault management is authorized for this administrator via vault:manage: use manage_vault_access for MCP connections and immediate tool/regex permissions, manage_vault_groups for groups/members, and manage_vault_secret_access for secret names and group grants. These are global tools and need no workflow_id. Regex rules require human-readable descriptions. Secret values are never returned. The same Vault management tools are declared in Vault product.yaml for builder and MCP. Use query_vault_db/mutate_vault_db for guarded governance SQL, list_vault_mcp_servers for setup inventory and call_vault_mcp_tool for approved upstream setup calls as this administrator. Ordinary Vault MCP runtime remains group scoped."
		}
	}
	for _, tool := range allowed {
		if tool.Name == "get_token_usage" {
			instructions += " Shared-account token limits: get_token_usage shows each person's tokens on the server's shared accounts today and this week (UTC, Monday weeks) against their daily/weekly limits, or totals for a from/to range; set_token_limits (administrators, users:manage) changes a person's limits (0 or null = unlimited, omitted = unchanged; in a person's account override 0 falls back to the account default and -1 is unlimited even when the account has a default). Allowed models: get_token_usage also lists each shared account's models (account_allowed_models) and each person's effective models per account (allowed_models); set_allowed_models (administrators, users:manage) sets an account's list (null = all models) or, with a person, their override (a list replaces the account's; null clears it; [\"*\"] or all_models = every model). Every call is recorded in the Code review audit log."
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
		if tool.Name == "create_workflow" {
			instructions += " Workflow creation: use create_workflow with folder_name, workflow_json and plan_json. Account creation rights and unrestricted Builder consent are required. The new workflow belongs to this user; use its returned workflow_id for builder_chat and authorized KB project bindings. Creation does not run it."
		}
		if tool.Name == "run_relay" {
			instructions += externalMCPRelayInstructions
		}
		if tool.Name == "builder_chat" {
			instructions += " Builder: use builder_chat to send editing requests to the configured model in your existing workflow chat. Use a unique submission_id, poll builder_status by operation_id, and answer pending questions with builder_reply_input. builder_cancel stops only that operation. Check get_agent_context(workflow_id) for current effective tools."
			break
		}
	}
	index, fingerprint := externalMCPToolIndex(externalListedTools(claims, allowed))
	mcpServer := server.NewMCPServer("AgentWorks", "1.0.0",
		server.WithToolCapabilities(false),
		server.WithElicitation(),
		server.WithInstructions(instructions),
	)
	for _, name := range []string{externalMCPToolSpec, externalMCPToolCall} {
		schema, err := json.Marshal(externalMCPToolSchemas[name])
		if err != nil {
			externalError(w, http.StatusInternalServerError, "schema_error", err.Error())
			return
		}
		mcpServer.AddTool(
			mcp.NewToolWithRawSchema(name, externalMCPToolDescriptionFor(name, index), schema),
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
	args := request.GetArguments()
	if args == nil {
		args = map[string]any{}
	}
	if name == externalMCPToolSpec {
		return externalMCPAPISpec(args, externalListedTools(GetUserFromContext(r.Context()), allowed), allowed)
	}
	target, _ := args["name"].(string)
	target = knowledgebase.CanonicalToolName(strings.TrimSpace(target))
	if target == "" {
		return mcp.NewToolResultError("invalid_arguments: call_tool requires the tool name in \"name\" (discover names with get_api_spec)")
	}
	byName := make(map[string]externalTool, len(allowed))
	for _, tool := range allowed {
		byName[tool.Name] = tool
	}
	if _, ok := byName[target]; !ok {
		catalog, err := externalTools()
		if err == nil {
			for _, tool := range catalog {
				if tool.Name == target {
					return mcp.NewToolResultError("insufficient_scope: this connection may not call \"" + target + "\"")
				}
			}
		}
		return mcp.NewToolResultError("unknown_tool: \"" + target + "\" is not exposed by this API; call get_api_spec with no arguments for the available tools")
	}
	callArgs, _ := args["arguments"].(map[string]any)
	if callArgs == nil {
		callArgs = map[string]any{}
	}
	// A function call with a pending question may come back as an MCP form
	// for clients that declare elicitation (docs/design/mcp_elicitation.md).
	elicitation := api.externalMCPElicitation(r, byName)
	if request.Params.RequestState != "" {
		return elicitation.Resume(ctx, target, callArgs, request)
	}
	if len(request.Params.InputResponses) > 0 {
		return mcp.NewToolResultError("invalid_request_state: input responses need the matching requestState")
	}
	rec, err := api.externalMCPInvoke(ctx, r, target, callArgs)
	if err != nil {
		return mcp.NewToolResultError("failed to encode tool arguments: " + err.Error())
	}
	if rec.status >= 200 && rec.status < 300 {
		if result := elicitation.MaybeElicit(ctx, target, callArgs, rec.body.Bytes()); result != nil {
			return result
		}
	}
	return externalMCPDispatchResult(rec)
}

func (api *StreamingAPI) externalMCPInvoke(ctx context.Context, r *http.Request, target string, callArgs map[string]any) (*externalMCPRecorder, error) {
	body, err := json.Marshal(map[string]any{"name": target, "arguments": callArgs})
	if err != nil {
		return nil, err
	}
	sub := r.Clone(ctx)
	sub.Body = io.NopCloser(bytes.NewReader(body))
	sub.ContentLength = int64(len(body))
	rec := &externalMCPRecorder{header: http.Header{}}
	api.handleExternalCall(rec, sub)
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
// listed is what the connection is shown; allowed adds hidden aliases, whose
// schemas can still be read by name.
func externalMCPAPISpec(args map[string]any, listed, allowed []externalTool) *mcp.CallToolResult {
	byName := make(map[string]externalTool, len(allowed))
	for _, tool := range allowed {
		byName[tool.Name] = tool
	}
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
		tool, ok := byName[knowledgebase.CanonicalToolName(strings.TrimSpace(name))]
		if !ok {
			return mcp.NewToolResultError("unknown_tool: \"" + name + "\" is not available to this connection; call get_api_spec with no arguments for the available tools")
		}
		schemas[tool.Name] = map[string]any{"description": tool.Description, "inputSchema": tool.InputSchema}
	}
	result, err := mcp.NewToolResultJSON(map[string]any{"schemas": schemas})
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
	order := []string{"Goals", "Builder", "Pulse", "Relays", "Crews", "Dashboards", "Needs you", "Brain", "Vault", "Code review", "Account", "Help"}
	groups := map[string][]string{}
	names := make([]string, 0, len(allowed))
	for _, tool := range allowed {
		name := tool.Name
		names = append(names, name)
		group := "Goals"
		switch {
		case strings.Contains(name, "dashboard") || name == "get_report_link":
			group = "Dashboards"
		case strings.HasSuffix(name, "_needs_you"):
			group = "Needs you"
		case strings.HasPrefix(name, "builder_pulse") || name == "manage_pulse":
			group = "Pulse"
		case strings.HasPrefix(name, "builder_"):
			group = "Builder"
		case strings.Contains(name, "relay"):
			group = "Relays"
		case strings.Contains(name, "crew"):
			group = "Crews"
		case strings.HasPrefix(name, "brain_"):
			group = "Brain"
		case strings.Contains(name, "vault"):
			group = "Vault"
		case strings.Contains(name, "_code_"):
			group = "Code review"
		case name == "get_token_usage" || name == "set_token_limits" || name == "set_allowed_models":
			group = "Account"
		case name == "get_agent_context" || name == "get_skill" || strings.Contains(name, "guidance_topic"):
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
	sum := sha256.Sum256([]byte(strings.Join(names, "\n")))
	return b.String(), "aw1-" + hex.EncodeToString(sum[:12])
}

func externalMCPToolDescriptionFor(name, index string) string {
	if name != externalMCPToolSpec || index == "" {
		return externalMCPToolDescriptions[name]
	}
	return externalMCPToolDescriptions[name] + "\n\nTools you can use now (pass names to get_api_spec for their arguments, then run them with call_tool):" + index
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
