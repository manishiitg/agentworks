package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"sort"
	"strings"
	"sync"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/agentworksproduct"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

type externalTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	mutates     bool
	executes    bool
	plan        bool
	validator   *jsonschema.Schema
}

var externalCatalogOnce sync.Once
var externalCatalog []externalTool
var externalCatalogErr error

func externalString(description string) map[string]any {
	return map[string]any{"type": "string", "description": description, "minLength": 1}
}
func externalInteger(minimum, maximum int) map[string]any {
	return map[string]any{"type": "integer", "minimum": minimum, "maximum": maximum}
}
func externalTools() ([]externalTool, error) {
	externalCatalogOnce.Do(func() {
		var defined []externalTool
		add := func(name, description string, write, scoped bool, props map[string]any, required ...string) {
			if props == nil {
				props = map[string]any{}
			}
			if scoped {
				props["workflow_id"] = externalString("Workflow ID returned by list_workflows. Never a filesystem path.")
				required = append(required, "workflow_id")
			}
			req := make([]any, len(required))
			for i, r := range required {
				req[i] = r
			}
			defined = append(defined, externalTool{Name: name, Description: description, mutates: write, InputSchema: map[string]any{"type": "object", "properties": props, "required": req, "additionalProperties": false}})
		}
		page := func() map[string]any {
			return map[string]any{"limit": externalInteger(1, 200), "offset": externalInteger(0, 10000)}
		}
		p := page()
		p["query"] = externalString("Filter workflow labels and IDs.")
		add("list_workflows", "List workflows visible to the signed-in user.", false, false, p)
		add("get_workflow", "Read one workflow manifest and the caller's access level.", false, true, nil)
		for _, name := range []string{"list_files", "search_files"} {
			p = page()
			p["path"] = externalString("Workflow-relative directory; defaults to root.")
			p["depth"] = externalInteger(1, 8)
			p["glob"] = externalString("Optional file path glob relative to path, e.g. **/*.py. ** matches directories recursively. Filters results before pagination and content search.")
			required := []string{}
			if name == "search_files" {
				p["query"] = externalString("Case-insensitive literal text to find.")
				required = append(required, "query")
			}
			add(name, "Browse or search workflow files. Private paths and symbolic links are excluded. Results are bounded and paginated.", false, true, p, required...)
		}
		p = page()
		p["step_id"] = externalString("Optional plan step ID; limits the inventory to its saved code directory.")
		p["glob"] = externalString("Optional path glob inside the code directory; defaults to **/*.py.")
		add("list_step_code", "List saved Python code by workflow step, with plan step IDs and titles where available. Uses code/<step-id>/ for current workflows and learnings/<step-id>/ for legacy workflows.", false, true, p)
		add("get_file_link", "Get an existing file or folder’s authenticated browser preview URL. Files also include an authenticated download URL, size and content type. Links never contain credentials; recipients need workflow access.", false, true, map[string]any{"path": externalString("Workflow-relative file or folder path.")}, "path")
		add("read_file", "Read a workflow file up to 2 MiB. Binary content is base64.", false, true, map[string]any{"path": externalString("Workflow-relative file path.")}, "path")
		// Tokens read and run, like the Slack and WhatsApp run-mode channels:
		// file writes, plan mutations, and Builder execution are not exposed.
		// Catalog membership is admitted by product.yaml (chat.run
		// external_tools plus the run.tools proxy surface); these definitions
		// are implementations only.
		add("get_plan", "Read the plan and configuration.", false, true, nil)
		add("get_agent_context", "Describe this connection for an external agent: token capabilities, available tools, and guidance version. No workflow required; pass workflow_id for the caller's role on it.", false, false, map[string]any{"workflow_id": map[string]any{"type": "string", "description": "Optional workflow ID to report the caller's role on."}})
		add("list_guidance_topics", "List the server-owned external guidance topics and their descriptions.", false, false, nil)
		add("get_guidance_topic", "Read one external guidance topic rendered from the canonical builder reference. Load only topics relevant to the task.", false, false, map[string]any{"topic": externalString("Topic name from list_guidance_topics.")}, "topic")
		add("list_workflow_knowledge", "List a workflow's learnings, knowledgebase notes, workspace skills, and skill wiring (workflow-selected skills plus per-step enabled_skills). Page the file inventories with limit and offset; has_more signals another page.", false, true, page())
		add("read_workflow_knowledge", "Read one knowledge file: learnings/ or knowledgebase/ paths from the workflow, or skills/<folder>/<file> from the workspace skill catalog. Nothing else is addressable.", false, true, map[string]any{"path": externalString("Knowledge path: learnings/..., knowledgebase/..., or skills/<folder>/<file>.")}, "path")
		add("list_runs", "List saved run folders and their metadata files. Use get_run for a chosen run.", false, true, page())
		for _, name := range []string{"get_run", "get_logs"} {
			p = page()
			p["run_folder"] = externalString("Run directory relative to runs/, e.g. iteration-0/group-name.")
			description := "Inspect a saved run's files or log files; use read_file to retrieve selected content."
			if name == "get_run" {
				description = "Inspect a saved run's files and webhook deploy metadata (commit_sha, component, env, deployed_at) when present. Use read_file to retrieve selected content."
			}
			add(name, description, false, true, p, "run_folder")
		}
		// JSON-direct run operations. The four run.tools names keep these
		// native implementations instead of the generic proxy below;
		// run_status has no chat equivalent and is admitted by external_tools.
		addRun := func(name, description string, executes bool, props map[string]any, required ...string) {
			if props == nil {
				props = map[string]any{}
			}
			props["workflow_id"] = externalString("Workflow ID returned by list_workflows. Never a filesystem path.")
			required = append(required, "workflow_id")
			req := make([]any, len(required))
			for i, r := range required {
				req[i] = r
			}
			defined = append(defined, externalTool{Name: name, Description: description, executes: executes, InputSchema: map[string]any{"type": "object", "properties": props, "required": req, "additionalProperties": false}})
		}
		p = page()
		p["session_id"] = externalString("Run session ID returned by a previous run call.")
		p["since_index"] = externalInteger(-1, 1000000000)
		addRun("run_status", "Poll a run session started externally: session status, event page, pending inputs, and its active executions.", false, p, "session_id")
		addRun("list_workflow_functions", "List the workflow's functions: typed entry points (a route plus named inputs set as that run's variables) that callers invoke with call_workflow_function.", false, nil)
		addRun("call_workflow_function", "Call one of the workflow's functions (see list_workflow_functions). Inputs are checked first: a missing, unknown or mistyped input is refused before anything runs. Returns at once with status=running and a call_id for get_workflow_function_call (functions take minutes); pass wait_seconds to wait up to 25s for the run outcome (status, error, step outputs). Repeating the same call while it runs returns the same call_id. Requires the runs:execute scope and workflow write access.", true, map[string]any{
			"function":     externalString("Function name from list_workflow_functions."),
			"args":         map[string]any{"type": "object", "description": "Inputs matching the function's input schema."},
			"wait_seconds": map[string]any{"type": "integer", "minimum": 0, "maximum": externalCrewMaxWaitSeconds, "description": "Seconds to wait for the outcome before returning a call_id to poll (default 0: return at once; max 25)."},
		}, "function")
		addRun("get_workflow_function_call", "Poll a call started with call_workflow_function: status (queued, running, completed, failed), the run outcome, and any error.", false, map[string]any{"call_id": externalString("call_id returned by call_workflow_function.")}, "call_id")
		addRun("suggest_workflow_change", "Suggest a change to a workflow you can use: what it should do differently. It goes to the workflow owner's decisions panel for review; nothing changes until they act. Available to read-only users.", false, map[string]any{
			"suggestion": map[string]any{"type": "string", "description": "The requested change in plain words.", "maxLength": 4000},
			"reason":     map[string]any{"type": "string", "description": "Optional short reason or example.", "maxLength": 4000},
			"step_id":    map[string]any{"type": "string", "description": "Optional related workflow step ID.", "maxLength": 200},
		}, "suggestion")
		addRun("list_executions", "List the workflow's active executions: execution and session IDs, step, status, and run folder.", false, nil)
		addRun("list_schedules", "List the workflow's schedules: IDs, type, cron or calendar shape, timezone, enabled state, and groups.", false, nil)
		p = page()
		p["schedule_id"] = externalString("Schedule ID from list_schedules.")
		p["offset"] = externalInteger(0, 1000000)
		addRun("get_schedule_runs", "Page a schedule's retained run history with limit and offset, including after schedule deletion: status, duration, run folder, errors, and webhook deploy metadata when present. Workflow runs are kept for at least 90 days.", false, p, "schedule_id")
		addRun("trigger_schedule", "Trigger a schedule to run immediately, outside its normal timing. Requires the runs:execute scope.", true, map[string]any{"schedule_id": externalString("Schedule ID from list_schedules.")}, "schedule_id")
		addRun("chat", "Chat with the workflow assistant in a pinned Run-mode session: ask questions, request analysis, or direct runs conversationally. Starts a new session, or continues session_id for multi-turn conversation. Requires the runs:execute scope. Poll run_status for the reply.", true, map[string]any{"message": externalString("The question or instruction to send."), "session_id": map[string]any{"type": "string", "description": "Existing run session ID to continue. Omit to start a new conversation."}}, "message")
		addRun("run_reply_input", "Answer a pending human-input request in a run session (see run_status pending_inputs). Requires the runs:execute scope.", true, map[string]any{"session_id": externalString("Run session ID from run_status."), "request_id": externalString("Pending input request ID from run_status."), "response": externalString("The answer to submit.")}, "session_id", "request_id", "response")
		// Stop commands execute directly instead of through the assistant
		// proxy: halting the wrong execution (or none) is not acceptable.
		addRun("stop_step", "Stop one running step or background execution by its execution ID from execute_step, query_step, or list_executions. The execution's session must belong to this connection.", true, map[string]any{"execution_id": externalString("Execution ID returned by a run tool or query_step."), "session_id": map[string]any{"type": "string", "description": "Optional run session ID; the execution must belong to it."}}, "execution_id")
		addRun("stop_all_executions", "Stop all running executions owned by this connection in the workflow, or one session when session_id is given. Executes directly.", true, map[string]any{"session_id": map[string]any{"type": "string", "description": "Optional run session ID to stop instead of every owned session."}})
		// Crews: bounded by the token's Crew list; crews:read.
		crewID := func(props map[string]any) map[string]any {
			if props == nil {
				props = map[string]any{}
			}
			props["crew_id"] = externalString("Crew ID returned by list_crews.")
			return props
		}
		add("list_crews", "List the Crews this connection may use: ID, name, identity, owner. Requires crews:read.", false, false, map[string]any{"query": externalString("Filter by Crew name, identity, or ID.")})
		add("get_crew", "Describe one Crew: identity, description, model, and its functions (typed entry points other Crews and connections can call). Requires crews:read.", false, false, crewID(nil), "crew_id")
		crewFiles := func(search bool) map[string]any {
			p := page()
			p["path"] = externalString("Crew-relative directory to start from; defaults to the Crew root.")
			p["depth"] = externalInteger(1, 8)
			p["glob"] = externalString("Optional path glob relative to path; use it to find files by name, e.g. **/*ForgotPassword*. ** matches directories recursively.")
			if search {
				p["query"] = externalString("Case-insensitive literal text to find inside files.")
			}
			return crewID(p)
		}
		add("list_crew_files", "List a Crew's project files (crew-relative paths), paginated. Start from a folder with path, go deeper with depth (up to 8), and find files by name with glob (e.g. **/*Login*). Private areas (chat transcripts under builder/, db/, the Crew's manifests, hidden folders) are never listed. Requires crews:read.", false, false, crewFiles(false), "crew_id")
		add("search_crew_files", "Search the text inside a Crew's project files for query (case-insensitive), optionally limited to path and glob. Returns matching files and lines, paginated. Private areas are never searched. Requires crews:read.", false, false, crewFiles(true), "crew_id", "query")
		add("read_crew_file", "Read one text file from a Crew's project (crew-relative path, up to 256 KiB). Private areas are refused. Requires crews:read.", false, false, crewID(map[string]any{"path": externalString("Crew-relative file path from list_crew_files.")}), "crew_id", "path")
		add("list_crew_functions", "List a Crew's functions: name, description, input and result schemas, including the built-in ask. Requires crews:read.", false, false, crewID(nil), "crew_id")
		// crews:run — the call runs as a turn in this user's own continuing
		// conversation with the Crew, never its main chat.
		wait := map[string]any{"type": "integer", "minimum": 0, "maximum": externalCrewMaxWaitSeconds, "description": "Seconds to wait for the result before returning a call_id to poll (default 0: return at once; max 25, proxies cut requests near 30s)."}
		add("call_crew_function", "Call one of a Crew's functions (see list_crew_functions) with arguments matching its input schema. The Crew does the work in your own continuing conversation with it (never its main chat); the result is validated against the function's result schema. Returns at once with status=running and a call_id for get_crew_function_call (functions take minutes); pass wait_seconds to wait up to 25s for the result. Repeating the same call while it runs returns the same call_id. Requires crews:run.", false, false, crewID(map[string]any{"function": externalString("Function name from list_crew_functions."), "args": map[string]any{"type": "object", "description": "Arguments matching the function's input schema."}, "wait_seconds": wait}), "crew_id", "function")
		add("ask_crew", "Ask a Crew anything in free text (its built-in ask function); the answer is its final reply. It is your message in your own chat of that Crew (the chat your web chat, Slack DMs and WhatsApp continue; for your own Crew, its main chat), so you can chat with it and see the exchange in the app. Returns at once with status=running and a call_id for get_crew_function_call; pass wait_seconds to wait up to 25s for the answer. Requires crews:run.", false, false, crewID(map[string]any{"message": externalString("The question or task for the Crew."), "wait_seconds": wait}), "crew_id", "message")
		add("suggest_crew_change", "Suggest a change to a Crew you use but do not own (its role, instructions, skills, functions, schedules or output). The owner reviews it in the Crew's Suggestions view; nothing changes until they act. Requires crews:run.", false, false, crewID(map[string]any{
			"suggestion": map[string]any{"type": "string", "description": "The requested change in plain words.", "maxLength": 4000},
			"reason":     map[string]any{"type": "string", "description": "Optional short reason or example.", "maxLength": 4000},
			"about":      map[string]any{"type": "string", "description": "Optional part of the Crew it concerns, e.g. a function or schedule name.", "maxLength": 200},
		}), "crew_id", "suggestion")
		add("get_crew_function_call", "Poll a call started with call_crew_function or ask_crew: status (queued, running, completed, failed), progress reports, and the result or error. Requires crews:read or crews:run.", false, false, map[string]any{"call_id": externalString("call_id returned by call_crew_function or ask_crew.")}, "call_id")
		// Crew authoring (crews:write; owner-only edits). One spec shape
		// serves create_crew, export_crew and import_crew.
		specProps, fnSpec, scheduleSpec := externalCrewSpecSchemas()
		add("create_crew", "Create a Crew you own from a spec: name, icon, role, purpose (its standing instructions), skills, functions, schedules, project files, and first-party templates. Returns the new Crew as get_crew does. Requires crews:write on a connection covering all your Crews.", false, false, specProps, "name", "role", "purpose")
		strList := map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
		add("update_crew", "Edit a Crew you own. Every field is optional; omitted parts stay as they are. skills: set/add/remove names. functions: upsert specs / delete names. schedules: add specs, update specs by id, remove ids. files: write project files; remove_files deletes them. Requires crews:write.", false, false, crewID(map[string]any{
			"name": map[string]any{"type": "string", "maxLength": 60}, "icon": map[string]any{"type": "string", "maxLength": 8},
			"role": map[string]any{"type": "string", "maxLength": 120}, "purpose": map[string]any{"type": "string", "maxLength": crewPurposeLimit},
			"skills":       map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"set": strList, "add": strList, "remove": strList}},
			"functions":    map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"upsert": map[string]any{"type": "array", "items": fnSpec}, "delete": strList}},
			"schedules":    map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"add": map[string]any{"type": "array", "items": scheduleSpec}, "update": map[string]any{"type": "array", "items": scheduleSpec}, "remove": strList}},
			"files":        specProps["files"],
			"remove_files": strList,
		}), "crew_id")
		add("export_crew", "Export a Crew as a portable spec (identity, skills with their project-local skill files, functions, schedules, template references). Chats, memory, databases, secrets, and model connections are never included. Pass the result to import_crew on any AgentWorks server. Requires crews:read.", false, false, crewID(nil), "crew_id")
		importSpec := map[string]any{"type": "object", "properties": specProps, "required": []any{"name", "role", "purpose"}, "description": "A spec from export_crew (or a Crew Agent Playbook catalog entry)."}
		add("import_crew", "Create a Crew you own from a spec produced by export_crew. Schedules arrive disabled unless enable_schedules is true. Requires crews:write on a connection covering all your Crews.", false, false, map[string]any{"spec": importSpec, "enable_schedules": map[string]any{"type": "boolean"}}, "spec")
		// Membership comes from product.yaml's run mode: external_tools
		// first, in yaml order, then every run.tools name (the single
		// source of truth for the run surface) that has no native
		// implementation above, proxied to a pinned Run-mode session in
		// yaml order. Go defines implementations (schemas, dispatch);
		// yaml admits them. Both mismatch directions fail here so drift
		// between the two can never ship silently.
		byName := make(map[string]externalTool, len(defined))
		for _, tool := range defined {
			byName[tool.Name] = tool
		}
		denied := make(map[string]bool)
		for _, name := range agentworksproduct.RunExternalDenylist() {
			denied[name] = true
		}
		admitted := agentworksproduct.RunExternalTools()
		seen := make(map[string]bool, len(admitted))
		for _, name := range admitted {
			if denied[name] {
				externalCatalogErr = fmt.Errorf("product.yaml both admits and withholds external tool %q", name)
				return
			}
			tool, ok := byName[name]
			if !ok {
				externalCatalogErr = fmt.Errorf("product.yaml admits unknown external tool %q", name)
				return
			}
			seen[name] = true
			externalCatalog = append(externalCatalog, tool)
		}
		runNames := agentworksproduct.RunTools()
		runSet := make(map[string]bool, len(runNames))
		for _, name := range runNames {
			runSet[name] = true
			if denied[name] {
				continue
			}
			if seen[name] {
				continue
			}
			seen[name] = true
			if native, ok := byName[name]; ok {
				externalCatalog = append(externalCatalog, native)
				continue
			}
			externalCatalog = append(externalCatalog, externalRunProxyTool(name))
		}
		for _, tool := range defined {
			if !seen[tool.Name] && !runSet[tool.Name] {
				externalCatalogErr = fmt.Errorf("external tool %q is implemented but admitted by neither product.yaml list", tool.Name)
				return
			}
		}
		for i := range externalCatalog {
			tool := &externalCatalog[i]
			compiler := jsonschema.NewCompiler()
			uri := "https://agentworks.invalid/schemas/" + tool.Name
			if err := compiler.AddResource(uri, tool.InputSchema); err != nil {
				externalCatalogErr = err
				return
			}
			validator, err := compiler.Compile(uri)
			if err != nil {
				externalCatalogErr = err
				return
			}
			tool.validator = validator
		}
	})
	return externalCatalog, externalCatalogErr
}
func externalError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": message}})
}
func externalJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
func (api *StreamingAPI) handleExternalTools(w http.ResponseWriter, r *http.Request) {
	if GetUserFromContext(r.Context()) == nil {
		externalError(w, 401, "unauthorized", "Sign in to AgentWorks.")
		return
	}
	catalog, err := externalTools()
	if err != nil {
		externalError(w, 500, "schema_error", err.Error())
		return
	}
	allowed := make([]externalTool, 0, len(catalog))
	for _, tool := range catalog {
		if externalTokenAllows(GetUserFromContext(r.Context()), tool) {
			allowed = append(allowed, tool)
		}
	}
	externalJSON(w, map[string]any{"tools": allowed})
}
func (api *StreamingAPI) handleExternalCall(w http.ResponseWriter, r *http.Request) {
	if GetUserFromContext(r.Context()) == nil {
		externalError(w, 401, "unauthorized", "Sign in to AgentWorks.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<20)
	var call struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&call); err != nil {
		externalError(w, 400, "invalid_arguments", err.Error())
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		externalError(w, 400, "invalid_arguments", "Expected one JSON object.")
		return
	}
	if call.Arguments == nil {
		call.Arguments = map[string]any{}
	}
	catalog, err := externalTools()
	if err != nil {
		externalError(w, 500, "schema_error", err.Error())
		return
	}
	var tool *externalTool
	for i := range catalog {
		if catalog[i].Name == call.Name {
			tool = &catalog[i]
			break
		}
	}
	if tool == nil {
		externalError(w, 404, "unknown_tool", "Tool is not exposed by this API.")
		return
	}
	if !externalTokenAllows(GetUserFromContext(r.Context()), *tool) {
		externalError(w, 403, "insufficient_scope", "This access token does not allow this operation.")
		return
	}
	if err = tool.validator.Validate(call.Arguments); err != nil {
		externalError(w, 400, "invalid_arguments", err.Error())
		return
	}
	if isExternalCrewTool(tool.Name) {
		api.externalCrewCall(w, r, tool.Name, call.Arguments)
		return
	}
	discovered, err := DiscoverWorkflowManifests(r.Context())
	if err != nil {
		externalError(w, 502, "workspace_unavailable", err.Error())
		return
	}
	visible := filterWorkflowManifestsForUser(GetUserFromContext(r.Context()), discovered)
	if token := GetUserFromContext(r.Context()).AccessToken; token != nil {
		filtered := make([]DiscoveredWorkflow, 0, len(visible))
		for _, workflow := range visible {
			if workflow.Manifest != nil && token.AllowsWorkflow(workflow.Manifest.ID) {
				filtered = append(filtered, workflow)
			}
		}
		visible = filtered
	}
	args := call.Arguments
	// Global tools need no workflow. They run before workflow resolution so a
	// restricted token can still obtain guidance and context.
	switch tool.Name {
	case "get_agent_context":
		api.externalAgentContext(w, r, args, visible)
		return
	case "list_guidance_topics":
		api.externalGuidanceTopicList(w, r)
		return
	case "get_guidance_topic":
		api.externalGuidanceTopicBody(w, r, args)
		return
	}
	if tool.Name == "list_workflows" {
		matches := make([]DiscoveredWorkflow, 0)
		query := strings.ToLower(externalArg(args, "query"))
		for _, item := range visible {
			if item.Manifest != nil && (query == "" || strings.Contains(strings.ToLower(item.Manifest.Label+" "+item.Manifest.ID), query)) {
				matches = append(matches, item)
			}
		}
		sort.Slice(matches, func(i, j int) bool { return matches[i].Manifest.ID < matches[j].Manifest.ID })
		start := min(externalInt(args, "offset", 0), len(matches))
		end := min(start+externalInt(args, "limit", 100), len(matches))
		externalJSON(w, map[string]any{"workflows": matches[start:end], "total": len(matches), "next_offset": end, "has_more": end < len(matches)})
		return
	}
	var selected *DiscoveredWorkflow
	for i := range visible {
		if visible[i].Manifest != nil && visible[i].Manifest.ID == externalArg(args, "workflow_id") {
			if selected != nil {
				externalError(w, 409, "ambiguous_workflow", "Duplicate workflow IDs must be resolved in AgentWorks.")
				return
			}
			selected = &visible[i]
		}
	}
	if selected == nil {
		externalError(w, 404, "workflow_not_found", "Workflow does not exist or is not accessible.")
		return
	}
	access := workflowAccessForManifest(GetUserFromContext(r.Context()), selected.Manifest)
	if tool.mutates && access != WorkflowAccessOwner && access != WorkflowAccessWrite {
		externalError(w, 403, "forbidden", "Workflow write access is required.")
		return
	}
	// Conversation access follows the builder runtime: workflow readers may
	// chat with its existing read-only tool policy, and control their own turns.
	if strings.HasPrefix(tool.Name, "builder_") {
		api.externalBuilderCall(w, r, tool.Name, args, *selected)
		return
	}
	if tool.Name == "get_file_link" {
		api.externalAssetLink(w, r, *selected, externalArg(args, "path"))
		return
	}
	if tool.Name == "list_workflow_knowledge" {
		api.externalListKnowledge(w, r, *selected, args)
		return
	}
	if tool.Name == "read_workflow_knowledge" {
		api.externalReadKnowledge(w, r, *selected, args)
		return
	}
	if tool.Name == "get_workflow" {
		externalJSON(w, selected)
		return
	}
	if tool.Name == "list_step_code" {
		api.externalListStepCode(w, r, *selected, args)
		return
	}
	if tool.Name == "suggest_workflow_change" {
		str := func(key string) string { value, _ := args[key].(string); return value }
		input, err := submitWorkflowSuggestion(r.Context(), GetUserFromContext(r.Context()), selected.WorkspacePath, "", str("suggestion"), str("reason"), str("step_id"))
		if err != nil {
			externalError(w, 400, "suggestion_refused", err.Error())
			return
		}
		externalJSON(w, map[string]any{"status": "submitted_for_owner_review", "workflow_id": selected.Manifest.ID, "suggestion_id": input.ID})
		return
	}
	switch tool.Name {
	case "list_workflow_functions", "call_workflow_function", "get_workflow_function_call":
		api.externalWorkflowFunctionCall(w, r, tool.Name, args, *selected, access)
		return
	}
	// Run operations dispatch before the workflow lock: proxy turns forward
	// to the asynchronous query runtime (which must never run under this
	// lock), and the status and schedule readers need no lock.
	switch tool.Name {
	case "run_status", "list_executions", "list_schedules", "get_schedule_runs", "trigger_schedule", "chat", "run_reply_input", "stop_step", "stop_all_executions":
		api.externalRunCall(w, r, tool.Name, args, *selected)
		return
	}
	if tool.executes {
		api.externalRunProxy(w, r, tool.Name, args, *selected)
		return
	}
	if tool.Name == "get_plan" {
		api.externalPlanCall(w, r, *selected)
		return
	}
	api.externalFileCall(w, r, tool.Name, args, *selected)
}
func externalArg(args map[string]any, name string) string { s, _ := args[name].(string); return s }
func externalInt(args map[string]any, name string, fallback int) int {
	switch n := args[name].(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return fallback
}

type externalUpstreamError struct {
	status  int
	message string
}

func (e *externalUpstreamError) Error() string { return e.message }
func externalFailure(w http.ResponseWriter, err error) {
	status := 502
	code := "workspace_error"
	var upstream *externalUpstreamError
	if errors.As(err, &upstream) {
		status = upstream.status
		switch status {
		case 409:
			code = "revision_conflict"
		case 403:
			code = "protected_path"
		case 400:
			code = "invalid_arguments"
		case 404:
			code = "not_found"
		case 413:
			code = "too_large"
		}
	} else if errors.Is(err, os.ErrNotExist) {
		status = http.StatusNotFound
		code = "not_found"
	}
	externalError(w, status, code, err.Error())
}
func (api *StreamingAPI) externalFileCall(w http.ResponseWriter, r *http.Request, name string, args map[string]any, workflow DiscoveredWorkflow) {
	req := wf.Request{Root: workflow.WorkspacePath, Path: externalArg(args, "path"), Query: externalArg(args, "query"), Glob: externalArg(args, "glob"), Offset: externalInt(args, "offset", 0), Limit: externalInt(args, "limit", 100), Depth: externalInt(args, "depth", 4)}
	switch name {
	case "read_file":
		req.Operation = "read"
	case "list_files":
		req.Operation = "list"
	case "search_files":
		req.Operation = "search"
	case "list_runs":
		req.Operation = "list"
		req.Path = "runs"
		req.Depth = 2
	case "get_run", "get_logs":
		folder, e := wf.CleanRelative(externalArg(args, "run_folder"))
		if e != nil {
			externalError(w, 400, "invalid_arguments", e.Error())
			return
		}
		req.Operation = "list"
		req.Path = path.Join("runs", folder)
		req.Depth = 4
		if name == "get_logs" {
			req.Path = path.Join(req.Path, "logs")
		}
	}
	result, err := externalFileRequest(r.Context(), req)
	if err != nil {
		externalFailure(w, err)
		return
	}
	// File reads include the existing Share file viewer URL. get_run
	// also adds the matching schedule run's delivery metadata.
	if (req.Operation == "read" && result.Exists) || name == "get_run" {
		raw, _ := json.Marshal(result)
		var linked map[string]any
		_ = json.Unmarshal(raw, &linked)
		linked["workflow_id"] = workflow.Manifest.ID
		if req.Operation == "read" && result.Exists {
			linked["preview_url"] = sharedAssetURL(r, path.Join(workflow.WorkspacePath, result.Path))
		}
		if name == "get_run" {
			folder := strings.Split(externalArg(args, "run_folder"), "/")[0]
			if webhookFolderPattern.MatchString(folder) {
				runs, err := ReadScheduleRuns(r.Context(), workflow.WorkspacePath)
				if err != nil {
					externalError(w, http.StatusBadGateway, "workspace_unavailable", "Webhook run history is unavailable.")
					return
				}
				for _, run := range runs {
					if run.RunFolder == folder && run.Webhook != nil {
						linked["schedule_run_id"] = run.ID
						linked["webhook"] = run.Webhook
						break
					}
				}
			}
		}
		externalJSON(w, linked)
		return
	}
	externalJSON(w, result)
}

var externalPlanPaths = []string{"planning/plan.json", "planning/step_config.json"}

func (api *StreamingAPI) externalPlanCall(w http.ResponseWriter, r *http.Request, workflow DiscoveredWorkflow) {
	artifacts := map[string]any{}
	var revisions strings.Builder
	for _, p := range externalPlanPaths {
		result, err := externalFileRequest(r.Context(), wf.Request{Root: workflow.WorkspacePath, Operation: "read", Path: p})
		if err != nil {
			externalFailure(w, err)
			return
		}
		revisions.WriteString(p + ":" + result.Revision + "\n")
		var value any
		if result.Exists {
			if err := json.Unmarshal([]byte(result.Content), &value); err != nil {
				externalFailure(w, fmt.Errorf("invalid JSON in %s: %w", p, err))
				return
			}
		}
		artifacts[p] = value
	}
	externalJSON(w, map[string]any{"workflow_id": workflow.Manifest.ID, "revision": wf.Revision([]byte(revisions.String())), "plan": artifacts["planning/plan.json"], "artifacts": artifacts})
}
