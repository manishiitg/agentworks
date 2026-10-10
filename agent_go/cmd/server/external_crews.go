package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/codeproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/terminals"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// External (MCP / agentworks CLI) access to Crews. Every tool resolves the
// Crew through the same server-wide Crew listing the app uses, then applies
// the token's Crew bound (crew_ids / all_crews). Files use the shared-Crew
// reader view, so private areas (builder/ transcripts, db/, manifests) are
// never exposed, whoever owns the Crew.

var externalCrewTools = map[string]bool{
	"list_crews": true, "get_crew": true, "list_crew_files": true, "search_crew_files": true, "read_crew_file": true, "list_crew_functions": true,
	"call_crew_function": true, "ask_crew": true, "get_crew_function_call": true, "reply_crew_function_call": true, "list_crew_function_calls": true, "write_crew_file": true, "suggest_crew_change": true,
	// Authoring (external_crew_authoring.go): export reads; the rest need crews:write.
	"create_crew": true, "update_crew": true, "export_crew": true, "import_crew": true,
	// Costs of a Crew you own (external_crew_costs.go).
	"get_crew_costs": true,
}

const (
	// externalCrewMaxWaitSeconds keeps a waiting request under the ~30s
	// origin timeout of the proxies in front of hosted deployments.
	externalCrewMaxWaitSeconds = 25
	externalCrewCallTimeout    = 60 * time.Minute
)

// externalCrewCaller stamps calls from this signed-in user's external
// connection; triggers bound to it are reused across that user's tokens.
func externalCrewCaller(claims *UserClaims) triggerLinkCaller {
	label := "external connection of " + firstNonEmptyTrimmed(claims.Username, claims.Email, claims.UserID)
	if claims.AccessToken != nil && strings.TrimSpace(claims.AccessToken.Name) != "" {
		label += " (" + strings.TrimSpace(claims.AccessToken.Name) + ")"
	}
	return triggerLinkCaller{Stamp: triggerCaller{Type: triggerCallerUser, ID: claims.UserID}, Label: label}
}

// externalCrewWait reads wait_seconds. Functions are agentic and usually
// take minutes, so a call returns at once unless the caller asks to wait
// (capped under the proxy timeout); a client whose request is cut short never
// sees the call_id and calls again.
func externalCrewWait(args map[string]any) time.Duration {
	raw, ok := args["wait_seconds"].(float64)
	if !ok || raw <= 0 {
		return 0
	}
	if raw > externalCrewMaxWaitSeconds {
		raw = externalCrewMaxWaitSeconds
	}
	return time.Duration(raw * float64(time.Second))
}

// externalCrewCallResponse returns the call's state after waiting up to wait.
func externalCrewCallResponse(ctx context.Context, call *crewFunctionCall, wait time.Duration) map[string]interface{} {
	if wait > 0 {
		timer := time.NewTimer(wait)
		select {
		case <-call.done:
		case <-timer.C:
		case <-ctx.Done():
		}
		timer.Stop()
	}
	out := call.snapshot()
	addFunctionCallPending(out, call)
	if status, _ := out["status"].(string); status != "completed" && status != "failed" {
		// A poll that waits is cheap: it returns the moment the call finishes (PLAT-837).
		where := "in the Crew's chat"
		if call.IsolatedExecution {
			where = "in its own isolated run"
		}
		out["next"] = fmt.Sprintf("Still running %s. Read the call again with this call_id and wait_seconds=%d (get_crew_function_call, or functions action=status): it returns as soon as the call finishes, or after that long with the progress so far, so poll again.", where, externalCrewMaxWaitSeconds)
	}
	return out
}

func isExternalCrewTool(name string) bool { return externalCrewTools[name] }

// externalCrewsVisible lists the Crews this connection may use.
func (api *StreamingAPI) externalCrewsVisible(ctx context.Context, claims *UserClaims, query string) ([]map[string]interface{}, error) {
	// An account without the Crew product sees no Crews: names, owners' emails and access levels are not for it
	// (a Code-only member listed all of them; PLAT-820).
	if api == nil || api.agentProfiles == nil {
		return nil, nil
	}
	if profile, err := api.agentProfiles.Resolve("work", 0, claims.UserID); err != nil || !userAllowedProduct(claims, profile.Product) {
		return []map[string]interface{}{}, nil
	}
	crews, err := listAccessibleCrewProjects(ctx, claims.UserID, strings.ToLower(strings.TrimSpace(query)))
	if err != nil {
		return nil, err
	}
	if claims.AccessToken == nil {
		return crews, nil
	}
	visible := make([]map[string]interface{}, 0, len(crews))
	for _, crew := range crews {
		if claims.AccessToken.AllowsCrew(fmt.Sprint(crew["id"])) {
			visible = append(visible, crew)
		}
	}
	return visible, nil
}

// externalCrewResolve returns the Crew binding for crew_id when this
// connection may use it. Unknown and out-of-bound Crews are one not-found.
func (api *StreamingAPI) externalCrewResolve(ctx context.Context, claims *UserClaims, crewID string) (crewProjectBinding, productProjectManifest, map[string]interface{}, bool) {
	crewID = strings.TrimSpace(crewID)
	if crewID == "" || api == nil || api.agentProfiles == nil {
		return crewProjectBinding{}, productProjectManifest{}, nil, false
	}
	crews, err := api.externalCrewsVisible(ctx, claims, "")
	if err != nil {
		return crewProjectBinding{}, productProjectManifest{}, nil, false
	}
	var summary map[string]interface{}
	for _, crew := range crews {
		if fmt.Sprint(crew["id"]) == crewID {
			summary = crew
			break
		}
	}
	if summary == nil {
		return crewProjectBinding{}, productProjectManifest{}, nil, false
	}
	profile, err := api.agentProfiles.Resolve("work", 0, claims.UserID)
	if err != nil || !userAllowedProduct(claims, profile.Product) {
		return crewProjectBinding{}, productProjectManifest{}, nil, false
	}
	crew, err := resolveCrewProjectBinding(ctx, claims.UserID, profile, crewID, "")
	if err != nil {
		return crewProjectBinding{}, productProjectManifest{}, nil, false
	}
	manifest, err := readCrewProjectManifests(ctx, profile.ID, crew.Binding.WorkspacePath)
	if err != nil || strings.TrimSpace(manifest.ID) != crewID {
		return crewProjectBinding{}, productProjectManifest{}, nil, false
	}
	return crew, manifest, summary, true
}

func externalCrewFunctionSummaries(ctx context.Context, crew crewProjectBinding, manifest productProjectManifest, label string) []map[string]interface{} {
	target := triggerTarget{Kind: triggerCallerCrew, Path: crew.Binding.WorkspacePath, Label: label, CrewID: manifest.ID, CrewProfile: "work", CrewOwner: crew.OwnerID}
	functions, err := readCrewFunctions(ctx, target)
	if err != nil {
		functions = nil
	}
	out := []map[string]interface{}{}
	for _, fn := range offeredCrewFunctions(ctx, target, functions) {
		out = append(out, map[string]interface{}{
			"name": fn.Name, "description": fn.Description, "instructions": fn.Instructions,
			"input_schema": fn.InputSchema, "result_schema": fn.ResultSchema,
		})
	}
	return out
}

func (api *StreamingAPI) externalCrewCall(w http.ResponseWriter, r *http.Request, name string, args map[string]any) {
	ctx := r.Context()
	claims := GetUserFromContext(ctx)
	str := func(key string) string {
		value, _ := args[key].(string)
		return strings.TrimSpace(value)
	}
	switch name {
	case "create_crew", "update_crew", "export_crew", "import_crew", "write_crew_file":
		api.externalCrewAuthoringCall(w, r, name, args)
		return
	}
	if name == "get_crew_costs" {
		api.externalCrewCosts(w, r, args)
		return
	}
	if name == "get_crew_function_call" || name == "reply_crew_function_call" {
		if name == "reply_crew_function_call" && claims.AccessToken != nil && !claims.AccessToken.Allows("crews:run") {
			externalError(w, 403, "insufficient_scope", "Answering a Crew question needs crews:run.")
			return
		}
		call := lookupCrewFunctionCall(str("call_id"))
		if call == nil {
			externalError(w, 404, "not_found", "Function call not found.")
			return
		}
		call.mu.Lock()
		// Only this user's own calls to a Crew: a workflow call is read with
		// get_workflow_function_call, under workflow access. Without the kind
		// check a Crew-only token read the user's workflow results (PLAT-366).
		mine := call.UserID == claims.UserID && call.CallerKind == triggerCallerUser
		isCrew := call.TargetKind == triggerCallerCrew && call.TargetProfileID != codeproduct.ProfileID
		targetID := call.TargetID
		call.mu.Unlock()
		if !isCrew || (claims.AccessToken != nil && !claims.AccessToken.AllowsCrew(targetID)) {
			externalError(w, 404, "not_found", "Function call not found.")
			return
		}
		// The caller must still be able to use that Crew now, not only when
		// the call was made.
		callCrew, _, _, ok := api.externalCrewResolve(ctx, claims, targetID)
		if !ok || !mine && (name == "reply_crew_function_call" || !callCrew.OwnedByCaller) {
			externalError(w, 404, "not_found", "Function call not found.")
			return
		}
		if name == "reply_crew_function_call" {
			replyFunctionCallInput(w, call, str("request_id"), str("response"))
			return
		}
		out := externalCrewCallResponse(ctx, call, externalCrewWait(args))
		if !api.externalFunctionReadDetails(w, ctx, args, call, out) {
			return
		}
		api.addCrewCallCommands(ctx, callCrew.OwnedByCaller, call, out)
		externalJSON(w, out)
		return
	}
	if name == "list_crews" {
		crews, err := api.externalCrewsVisible(ctx, claims, str("query"))
		if err != nil {
			externalError(w, 502, "workspace_unavailable", "Cannot list Crews.")
			return
		}
		items := make([]map[string]interface{}, 0, len(crews))
		for _, crew := range crews {
			items = append(items, map[string]interface{}{
				"crew_id": crew["id"], "name": crew["name"], "identity": crew["identity"],
				"owner": crew["owner"], "access": externalCrewAccessLabel(crew["access"]),
			})
		}
		externalJSON(w, map[string]any{"crews": items})
		return
	}
	crew, manifest, summary, ok := api.externalCrewResolve(ctx, claims, str("crew_id"))
	if !ok {
		externalError(w, 404, "not_found", "Crew not found or not allowed for this connection.")
		return
	}
	label := fmt.Sprint(summary["name"])
	switch name {
	case "get_crew":
		externalJSON(w, externalCrewDescription(ctx, crew, manifest, summary))
	case "suggest_crew_change":
		// Stored at the owner's physical Crew root, where the owner reviews it.
		crewPath := agentProfileRuntimeWorkspace(crew.OwnerID, crew.Binding.WorkspacePath)
		input, err := submitCrewSuggestion(ctx, claims, crewPath, "", str("suggestion"), str("reason"), str("about"))
		if err != nil {
			externalError(w, 400, "suggestion_refused", err.Error())
			return
		}
		externalJSON(w, map[string]any{"status": "submitted_for_owner_review", "crew_id": manifest.ID, "suggestion_id": input.ID})
	case "ask_crew":
		target := triggerTarget{Kind: triggerCallerCrew, Path: crew.Binding.WorkspacePath, Label: label, CrewID: manifest.ID, CrewProfile: "work", CrewOwner: crew.OwnerID}
		api.externalSendMessage(w, r, args, target)
	case "call_crew_function":
		target := triggerTarget{Kind: triggerCallerCrew, Path: crew.Binding.WorkspacePath, Label: label, CrewID: manifest.ID, CrewProfile: "work", CrewOwner: crew.OwnerID}
		functions, err := readCrewFunctions(ctx, target)
		if err != nil {
			externalError(w, 502, "workspace_unavailable", "Cannot read the Crew's functions.")
			return
		}
		fnName := str("function")
		callArgs, _ := args["args"].(map[string]interface{})
		fn, found := findCrewFunction(offeredCrewFunctions(ctx, target, functions), fnName)
		if !found {
			externalError(w, 404, "not_found", fmt.Sprintf("Crew %q has no function %q; see list_crew_functions.", label, fnName))
			return
		}
		// Each declared function gets a fresh isolated trigger execution.
		callCtx := context.WithoutCancel(ctx)
		// An owner can run a call in Run mode to see how it behaves for anyone else (PLAT-756). It only narrows the turn.
		if runMode, _ := args["run_mode"].(bool); runMode {
			callCtx = withCrewRunMode(callCtx)
		}
		submissionID, _ := args["submission_id"].(string)
		call, err := api.startCrewFunctionCall(callCtx, claims.UserID, externalCrewCaller(claims), target, fn, callArgs, externalCrewCallTimeout, submissionID)
		if err != nil {
			status := http.StatusBadRequest
			if strings.Contains(err.Error(), "busy") {
				status = http.StatusConflict
			}
			externalError(w, status, "call_refused", err.Error())
			return
		}
		out := externalCrewCallResponse(ctx, call, externalCrewWait(args))
		api.addCrewCallCommands(ctx, crew.OwnedByCaller, call, out)
		externalJSON(w, out)
	case "list_crew_function_calls":
		loadExternalSavedFunctionCalls(ctx)
		externalJSON(w, map[string]any{"crew_id": manifest.ID, "calls": externalCrewFunctionCalls(manifest.ID, claims.UserID, crew.OwnedByCaller, externalInt(args, "limit", crewFunctionRecentCallsLimit))})
	case "list_crew_functions":
		externalJSON(w, map[string]any{"crew_id": manifest.ID, "functions": externalCrewFunctionSummaries(ctx, crew, manifest, label)})
	case "list_crew_files", "search_crew_files":
		// The workflow file engine: folder, depth, glob, pagination and text
		// search. A fixed 4-level, 1,000-entry listing could not reach files
		// inside the repositories a Crew clones (server A 2026-09-28: SDE).
		operation := "list"
		if name == "search_crew_files" {
			operation = "search"
		}
		result, err := externalFileRequest(ctx, wf.Request{
			Root: agentProfileRuntimeWorkspace(crew.OwnerID, crew.Binding.WorkspacePath), Operation: operation,
			Path: str("path"), Query: str("query"), Glob: str("glob"),
			Offset: externalInt(args, "offset", 0), Limit: externalInt(args, "limit", 100), Depth: externalInt(args, "depth", 4),
		})
		if err != nil {
			externalFailure(w, err)
			return
		}
		externalJSON(w, map[string]any{"crew_id": manifest.ID, "result": result})
	case "read_crew_file":
		if _, confined := confineSharedProjectPath(crew.Binding.WorkspacePath, str("path")); !confined {
			externalError(w, 404, "not_found", "File not found or private.")
			return
		}
		result, err := externalFileRequest(ctx, wf.Request{Root: agentProfileRuntimeWorkspace(crew.OwnerID, crew.Binding.WorkspacePath), Path: str("path"), Operation: "read"})
		if err != nil {
			externalFailure(w, err)
			return
		}
		if !result.Exists {
			externalError(w, 404, "not_found", "File not found or private.")
			return
		}
		out := map[string]any{"crew_id": manifest.ID, "path": str("path"), "encoding": result.Encoding, "size": result.Size, "revision": result.Revision, "truncated": false}
		if result.Encoding == "base64" {
			out["content_base64"] = result.Content
		} else {
			out["content"] = result.Content
		}
		externalJSON(w, out)
	default:
		externalError(w, 404, "unknown_tool", "Tool is not exposed by this API.")
	}
}

// externalCrewAccessLabel names what the caller can do in a Crew. The shared listing marks another owner's Crew
// "write" (Crew-to-Crew work); to an outside caller that overstates it: only the owner edits, everyone else runs it
// in Run mode (reads it, writes only its output folder).
func externalCrewAccessLabel(access interface{}) interface{} {
	if access == "write" {
		return "run"
	}
	return access
}

// externalCrewFunctionCalls lists recent calls to one Crew, newest first. The Crew's owner sees every call (who made it,
// which function, when, how it ended) but not the arguments or results of other people's calls; anyone else sees only the
// calls they made themselves, in full. Saved records are loaded before listing.
func externalCrewFunctionCalls(crewID, userID string, owner bool, limit int) []map[string]any {
	if limit <= 0 || limit > crewFunctionRecentCallsLimit {
		limit = crewFunctionRecentCallsLimit
	}
	crewFunctionCalls.Lock()
	calls := make([]*crewFunctionCall, 0, len(crewFunctionCalls.m))
	for _, call := range crewFunctionCalls.m {
		calls = append(calls, call)
	}
	crewFunctionCalls.Unlock()
	type row struct {
		started time.Time
		entry   map[string]any
	}
	rows := []row{}
	for _, call := range calls {
		call.mu.Lock()
		if call.TargetKind != triggerCallerCrew || call.TargetID != strings.TrimSpace(crewID) || call.TargetProfileID == codeproduct.ProfileID {
			call.mu.Unlock()
			continue
		}
		mine := call.UserID == userID
		if !mine && !owner {
			call.mu.Unlock()
			continue
		}
		entry := map[string]any{
			"call_id": call.ID, "function": call.Function, "status": call.Status, "started_at": call.CreatedAt,
			"caller": map[string]any{"kind": call.CallerKind, "name": call.CallerLabel, "you": mine},
		}
		if call.terminalLocked() {
			entry["finished_at"] = call.UpdatedAt
		}
		if mine {
			entry["answer"] = call.Answer
			entry["files"] = call.Files
			if call.Result != nil {
				entry["result"] = call.Result
			}
		}
		if call.Error != "" {
			entry["error"] = call.Error
		}
		started := call.CreatedAt
		call.mu.Unlock()
		rows = append(rows, row{started, entry})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].started.After(rows[j].started) })
	if len(rows) > limit {
		rows = rows[:limit]
	}
	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.entry)
	}
	return out
}

const crewCallCommandsLimit = 40

// addCrewCallCommands adds the commands the Crew's agent ran for a call to its reply, for the Crew's owner only: what the
// agent says it did can then be checked. Callers who are not the owner never get it. Secret shapes are masked the way the
// terminal view masks them, each command is cut to a few hundred characters, and only the latest ones are kept.
func (api *StreamingAPI) addCrewCallCommands(ctx context.Context, owner bool, call *crewFunctionCall, out map[string]interface{}) {
	if !owner || api == nil || call == nil {
		return
	}
	call.mu.Lock()
	sessionID, runID, caller, target, triggerID, userID := call.TargetChatSession, call.RunID, call.caller, call.target, call.TriggerID, call.UserID
	call.mu.Unlock()
	if sessionID == "" {
		if runID == "" {
			return
		}
		state, err := api.readTriggerTargetRun(ctx, userID, caller, target, triggerID, runID)
		if err != nil {
			return
		}
		sessionID = crewTargetRunSessionID(state)
	}
	if commands := api.crewSessionCommands(sessionID); len(commands) > 0 {
		out["commands_run"] = commands
	}
}

// crewSessionCommands lists the tools a session started, newest last: the shell command for a shell tool, only the name for
// any other tool. It reads the session's recorded events, which are bounded, so a long session shows its latest part.
func (api *StreamingAPI) crewSessionCommands(sessionID string) []map[string]interface{} {
	if api == nil || api.eventStore == nil || strings.TrimSpace(sessionID) == "" {
		return nil
	}
	commands := []map[string]interface{}{}
	for _, event := range api.eventStore.GetEvents(sessionID, storeGetEventsAll()).Events {
		if event.Data == nil || !strings.Contains(strings.ToLower(string(event.Data.Type)), "tool_call_start") {
			continue
		}
		fields := map[string]interface{}{}
		if encoded, err := json.Marshal(event.Data.Data); err != nil || json.Unmarshal(encoded, &fields) != nil {
			continue
		}
		tool, _ := fields["tool_name"].(string)
		if tool == "" {
			continue
		}
		entry := map[string]interface{}{"tool": tool, "at": event.Timestamp}
		if params, _ := fields["tool_params"].(map[string]interface{}); params != nil {
			var arguments struct {
				Command string `json:"command"`
			}
			if raw, _ := params["arguments"].(string); raw != "" && json.Unmarshal([]byte(raw), &arguments) == nil && strings.TrimSpace(arguments.Command) != "" {
				command := terminals.RedactSensitiveTerminalText(strings.TrimSpace(arguments.Command))
				if runes := []rune(command); len(runes) > 400 {
					command = string(runes[:400]) + "…"
				}
				entry["command"] = command
			}
		}
		commands = append(commands, entry)
	}
	if len(commands) > crewCallCommandsLimit {
		commands = commands[len(commands)-crewCallCommandsLimit:]
	}
	return commands
}
