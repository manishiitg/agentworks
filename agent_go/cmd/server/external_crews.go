package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// External (MCP / agentworks CLI) access to Crews. Every tool resolves the
// Crew through the same server-wide Crew listing the app uses, then applies
// the token's Crew bound (crew_ids / all_crews). Files use the shared-Crew
// reader view, so private areas (builder/ transcripts, db/, manifests) are
// never exposed, whoever owns the Crew.

var externalCrewTools = map[string]bool{
	"list_crews": true, "get_crew": true, "list_crew_files": true, "search_crew_files": true, "read_crew_file": true, "list_crew_functions": true,
	"call_crew_function": true, "ask_crew": true, "get_crew_function_call": true, "reply_crew_function_call": true, "suggest_crew_change": true,
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
		out["next"] = "Still running in the Crew's chat. Poll get_crew_function_call with this call_id."
	}
	return out
}

func isExternalCrewTool(name string) bool { return externalCrewTools[name] }

// externalCrewsVisible lists the Crews this connection may use.
func (api *StreamingAPI) externalCrewsVisible(ctx context.Context, claims *UserClaims, query string) ([]map[string]interface{}, error) {
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
	for _, fn := range withDefaultAskFunction(functions) {
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
	case "create_crew", "update_crew", "export_crew", "import_crew":
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
		owned := call.UserID == claims.UserID && call.CallerKind == triggerCallerUser && call.TargetKind == triggerCallerCrew
		targetID := call.TargetID
		call.mu.Unlock()
		if !owned || (claims.AccessToken != nil && !claims.AccessToken.AllowsCrew(targetID)) {
			externalError(w, 404, "not_found", "Function call not found.")
			return
		}
		// The caller must still be able to use that Crew now, not only when
		// the call was made.
		if _, _, _, ok := api.externalCrewResolve(ctx, claims, targetID); !ok {
			externalError(w, 404, "not_found", "Function call not found.")
			return
		}
		if name == "reply_crew_function_call" {
			replyFunctionCallInput(w, call, str("request_id"), str("response"))
			return
		}
		externalJSON(w, externalCrewCallResponse(ctx, call, 0))
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
				"owner": crew["owner"], "access": crew["access"],
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
	case "call_crew_function", "ask_crew":
		target := triggerTarget{Kind: triggerCallerCrew, Path: crew.Binding.WorkspacePath, Label: label, CrewID: manifest.ID, CrewProfile: "work", CrewOwner: crew.OwnerID}
		functions, err := readCrewFunctions(ctx, target)
		if err != nil {
			externalError(w, 502, "workspace_unavailable", "Cannot read the Crew's functions.")
			return
		}
		fnName, callArgs := crewFunctionAskName, map[string]interface{}{"message": str("message")}
		if name == "call_crew_function" {
			fnName = str("function")
			callArgs, _ = args["args"].(map[string]interface{})
		}
		fn, found := findCrewFunction(withDefaultAskFunction(functions), fnName)
		if !found {
			externalError(w, 404, "not_found", fmt.Sprintf("Crew %q has no function %q; see list_crew_functions.", label, fnName))
			return
		}
		// A side chat of this person's (manage_crew_chats) instead of the main one.
		if chat, ok := api.crewAskChat(r, claims.UserID, manifest.ID, crew.Binding.WorkspacePath, str("chat_id")); !ok {
			externalError(w, 404, "chat_not_found", "No chat with that chat_id; see manage_crew_chats list.")
			return
		} else if chat != nil {
			target.Chat = chat
			target.Label = label + " · " + chat.Name
		}
		// The call outlives this request; the Crew works in this user's own
		// conversation with it.
		callCtx := context.WithoutCancel(ctx)
		submissionID, _ := args["submission_id"].(string)
		call, err := api.startCrewFunctionCall(callCtx, claims.UserID, externalCrewCaller(claims), target, fn, callArgs, externalCrewCallTimeout, submissionID)
		if err != nil {
			externalError(w, 400, "call_refused", err.Error())
			return
		}
		externalJSON(w, externalCrewCallResponse(ctx, call, externalCrewWait(args)))
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
		full, confined := confineSharedProjectPath(crew.Binding.WorkspacePath, str("path"))
		if !confined {
			externalError(w, 404, "not_found", "File not found or private.")
			return
		}
		raw, found, err := readFileFromWorkspace(ctx, full)
		if err != nil || !found {
			externalError(w, 404, "not_found", "File not found or private.")
			return
		}
		if isSharedProjectBinaryContent(raw) {
			externalError(w, 415, "unsupported", "File is not readable as text.")
			return
		}
		truncated := false
		if len(raw) > sharedProjectFileContentCap {
			raw, truncated = raw[:sharedProjectFileContentCap], true
		}
		externalJSON(w, map[string]any{"crew_id": manifest.ID, "path": str("path"), "content": raw, "truncated": truncated})
	default:
		externalError(w, 404, "unknown_tool", "Tool is not exposed by this API.")
	}
}
