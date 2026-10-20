package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"unicode/utf8"

	"github.com/gorilla/mux"
)

// Dashboard function buttons (PLAT-750): a published dashboard lists and calls
// the functions of its own project. The project comes from the server-owned
// dashboard record, never from the page, and the call is the same typed-call
// path MCP and the Run chat use (access, caller policy, idempotency and call
// ownership are all checked there). Submit is the approval: there is no extra
// gate beyond the existing access rules.
func (api *StreamingAPI) registerDashboardFunctionRoutes(router *mux.Router) {
	router.HandleFunc("/dashboards/{dashboard}/functions", api.handleDashboardFunctionList).Methods(http.MethodGet)
	router.HandleFunc("/dashboards/{dashboard}/functions/call", api.handleDashboardFunctionCall).Methods(http.MethodPost)
	router.HandleFunc("/dashboards/{dashboard}/function-calls/{call}", api.handleDashboardFunctionCallRead).Methods(http.MethodGet)
}

type dashboardFunctionCallBody struct {
	Function     string         `json:"function"`
	Args         map[string]any `json:"args"`
	SubmissionID string         `json:"submission_id"`
}

// dashboardFunctionDispatch runs one external typed-call tool for the
// dashboard's project as the signed-in person and copies its JSON answer.
func (api *StreamingAPI) dashboardFunctionDispatch(w http.ResponseWriter, r *http.Request, resolved resolvedDashboardQuestion, crewTool, workflowTool string, args map[string]any) {
	ctx := r.Context()
	claims := GetUserFromContext(ctx)
	req := httptest.NewRequest(http.MethodPost, "/api/external/call", nil)
	req = req.WithContext(context.WithValue(ctx, UserContextKey, claims))
	rec := httptest.NewRecorder()
	switch resolved.Target.Kind {
	case "crew":
		origin := crewProfileID

		args["crew_id"] = workspaceExternalID(origin, resolved.Target.ID)
		api.externalCrewCall(rec, req, crewTool, args)
	case "workflow":
		access, manifest := workflowAccessForWorkspacePath(ctx, claims, resolved.Target.Root)
		if manifest == nil || manifest.Kind == "relay" || access == WorkflowAccessNone {
			externalError(w, 404, "not_found", "This dashboard has no functions to call.")
			return
		}
		api.externalWorkflowFunctionCall(rec, req, workflowTool, args, DiscoveredWorkflow{WorkspacePath: resolved.Target.Root, Manifest: manifest}, access)
	default:
		externalError(w, 404, "not_found", "This dashboard has no functions to call.")
		return
	}
	// A viewer who lost access to the Crew gets the same readable message as the
	// workflow branch, not the connection wording of the external tool.
	if rec.Code == http.StatusNotFound && strings.Contains(rec.Body.String(), "Crew not found or not allowed") {
		externalError(w, 404, "not_found", "You can no longer use this project's functions.")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(rec.Code)
	_, _ = w.Write(rec.Body.Bytes())
}

func (api *StreamingAPI) handleDashboardFunctionList(w http.ResponseWriter, r *http.Request) {
	resolved, ok := api.dashboardQuestionRequest(w, r, "", false)
	if !ok {
		return
	}
	api.dashboardFunctionDispatch(w, r, resolved, "list_crew_functions", "list_workflow_functions", map[string]any{})
}

func (api *StreamingAPI) handleDashboardFunctionCall(w http.ResponseWriter, r *http.Request) {
	var body dashboardFunctionCallBody
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil || decoder.Decode(new(any)) != io.EOF {
		externalError(w, 400, "invalid_arguments", "invalid function call")
		return
	}
	body.Function = strings.TrimSpace(body.Function)
	if body.Function == "" || len(body.Function) > 128 || body.SubmissionID == "" || len(body.SubmissionID) > 128 || !utf8.ValidString(body.Function) || !utf8.ValidString(body.SubmissionID) {
		externalError(w, 400, "invalid_arguments", "function and a stable submission id are required")
		return
	}
	resolved, ok := api.dashboardQuestionRequest(w, r, "", false)
	if !ok {
		return
	}
	args := map[string]any{"function": body.Function, "submission_id": body.SubmissionID, "wait_seconds": 2}
	if body.Args != nil {
		args["args"] = body.Args
	}
	api.dashboardFunctionDispatch(w, r, resolved, "call_crew_function", "call_workflow_function", args)
}

func (api *StreamingAPI) handleDashboardFunctionCallRead(w http.ResponseWriter, r *http.Request) {
	resolved, ok := api.dashboardQuestionRequest(w, r, "", false)
	if !ok {
		return
	}
	callID := strings.TrimSpace(mux.Vars(r)["call"])
	call := lookupCrewFunctionCall(callID)
	if call == nil {
		externalError(w, 404, "not_found", "Function call not found.")
		return
	}
	// A dashboard reads only calls to its own project, never another project's.
	call.mu.Lock()
	sameProject := call.TargetID == resolved.Target.ID
	call.mu.Unlock()
	if !sameProject {
		externalError(w, 404, "not_found", "Function call not found.")
		return
	}
	wait := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("wait_seconds")); raw != "" {
		for _, c := range raw {
			if c < '0' || c > '9' || wait > 25 {
				wait = 0
				break
			}
			wait = wait*10 + int(c-'0')
		}
		if wait > 25 {
			wait = 25
		}
	}
	api.dashboardFunctionDispatch(w, r, resolved, "get_crew_function_call", "get_workflow_function_call", map[string]any{"call_id": callID, "wait_seconds": wait})
}
