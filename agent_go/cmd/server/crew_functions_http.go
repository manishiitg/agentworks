package server

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gorilla/mux"
)

// Crew functions in the Automation panel (PLAT-357): the Functions tab lists
// a Crew's declared functions and recent calls
// made to them, including saved calls from before a restart. Deletion is limited to the
// Crew owner.

const crewFunctionRecentCallsLimit = 25

// crewFunctionTarget resolves the Crew a functions request is about through the shared-Crew lookup
// (resolveCrewProjectBinding: the caller's own Crew, else a Crew shared with them or that they co-own, else
// refused). Everyone it resolves for reads the functions; only the owner (or a joined Team member with owner rights,
// OwnedByCaller) may change them, which `manage` asks for. canManage tells the screen which it is. Other products
// (Code) keep the caller's own projects only.
func (s *ProjectActionDispatcher) crewFunctionTarget(r *http.Request, manage bool) (target triggerTarget, canManage bool, status int, message string) {
	profileID, projectID := productWebhookCoordinates(r)
	if profileID == "" {
		profileID = "work"
	}
	userID := productWorkspaceUserID(r.Context())
	if profileID != crewProfileID {
		_, binding, _, err := s.projectManifest(r.Context(), userID, profileID, projectID)
		if err != nil {
			return triggerTarget{}, false, http.StatusNotFound, err.Error()
		}
		return triggerTarget{
			Kind: triggerCallerCrew, Path: agentProfileRuntimeWorkspace(userID, binding.WorkspacePath),
			CrewID: projectID, CrewProfile: profileID, CrewOwner: userID,
		}, true, 0, ""
	}
	if s.registry == nil {
		return triggerTarget{}, false, http.StatusNotFound, "Crew not found"
	}
	profile, err := s.registry.Resolve(profileID, 0, userID)
	if err != nil {
		return triggerTarget{}, false, http.StatusNotFound, "Crew not found"
	}
	project, err := resolveCrewProjectBinding(r.Context(), userID, profile, projectID, "")
	if err != nil {
		return triggerTarget{}, false, http.StatusNotFound, "Crew not found"
	}
	if manage && !project.OwnedByCaller {
		return triggerTarget{}, false, http.StatusForbidden, "Only the Crew's owner can change its functions."
	}
	return triggerTarget{Kind: triggerCallerCrew, Path: project.Binding.WorkspacePath, CrewID: projectID, CrewProfile: profileID, CrewOwner: project.OwnerID}, project.OwnedByCaller, 0, ""
}

type crewFunctionCallView struct {
	CallID            string                   `json:"call_id"`
	Function          string                   `json:"function"`
	CallerKind        string                   `json:"caller_kind"`
	CallerLabel       string                   `json:"caller_label"`
	Status            string                   `json:"status"`
	LatestProgress    *crewFunctionProgress    `json:"latest_progress,omitempty"`
	Progress          []crewFunctionProgress   `json:"progress,omitempty"`
	Result            interface{}              `json:"result,omitempty"`
	Answer            string                   `json:"answer,omitempty"`
	Files             []structuredFunctionFile `json:"files,omitempty"`
	IsolatedExecution bool                     `json:"isolated_execution,omitempty"`
	CallerYou         bool                     `json:"-"`
	Error             string                   `json:"error,omitempty"`
	StartedAt         time.Time                `json:"started_at"`
	FinishedAt        *time.Time               `json:"finished_at,omitempty"`
}

// recentCrewFunctionCalls is the shared website/MCP history for an admitted
// Crew. Published function results belong to the project; private chat-to-chat
// calls remain outside its history.
func recentCrewFunctionCalls(ctx context.Context, targetID, requesterID string) []crewFunctionCallView {
	loadExternalSavedFunctionCalls(ctx)
	crewFunctionCalls.Lock()
	calls := make([]*crewFunctionCall, 0, len(crewFunctionCalls.m))
	for _, call := range crewFunctionCalls.m {
		calls = append(calls, call)
	}
	crewFunctionCalls.Unlock()
	views := []crewFunctionCallView{}
	for _, call := range calls {
		call.mu.Lock()
		if call.TargetKind != triggerCallerCrew || call.TargetID != strings.TrimSpace(targetID) || call.TargetChat != "" {
			call.mu.Unlock()
			continue
		}
		view := crewFunctionCallView{
			CallID: call.ID, Function: call.Function, CallerKind: call.CallerKind, CallerLabel: call.CallerLabel,
			Status: call.Status, Result: call.Result, Error: call.Error, StartedAt: call.CreatedAt,
			Progress: append([]crewFunctionProgress(nil), call.Progress...),
			Answer:   call.Answer, Files: append([]structuredFunctionFile(nil), call.Files...),
			IsolatedExecution: call.IsolatedExecution, CallerYou: call.UserID == strings.TrimSpace(requesterID),
		}
		if len(call.Progress) > 0 {
			latest := call.Progress[len(call.Progress)-1]
			view.LatestProgress = &latest
		}
		if call.terminalLocked() {
			finished := call.UpdatedAt
			view.FinishedAt = &finished
		}
		call.mu.Unlock()
		views = append(views, view)
	}
	sort.Slice(views, func(i, j int) bool { return views[i].StartedAt.After(views[j].StartedAt) })
	if len(views) > crewFunctionRecentCallsLimit {
		views = views[:crewFunctionRecentCallsLimit]
	}
	return views
}

func (s *ProjectActionDispatcher) listCrewFunctionsHTTP(w http.ResponseWriter, r *http.Request) {
	target, canManage, status, message := s.crewFunctionTarget(r, false)
	if status != 0 {
		http.Error(w, message, status)
		return
	}
	functions, err := readCrewFunctions(r.Context(), target)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	type functionView struct {
		crewFunction
		Implicit bool `json:"implicit,omitempty"`
	}
	views := []functionView{}
	for _, fn := range offeredCrewFunctions(r.Context(), target, functions) {
		_, declared := findCrewFunction(functions, fn.Name)
		views = append(views, functionView{crewFunction: fn, Implicit: !declared})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"can_manage": canManage, "functions": views, "calls": recentCrewFunctionCalls(r.Context(), target.CrewID, GetUserIDFromContext(r.Context()))})
}

func (s *ProjectActionDispatcher) deleteCrewFunctionHTTP(w http.ResponseWriter, r *http.Request) {
	target, _, status, message := s.crewFunctionTarget(r, true)
	if status != 0 {
		http.Error(w, message, status)
		return
	}
	name := strings.TrimSpace(mux.Vars(r)["name"])
	functions, err := readCrewFunctions(r.Context(), target)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	kept := make([]crewFunction, 0, len(functions))
	removed := false
	for _, fn := range functions {
		if fn.Name == name {
			removed = true
			continue
		}
		kept = append(kept, fn)
	}
	if !removed {
		http.Error(w, "function not found", http.StatusNotFound)
		return
	}
	if err := writeCrewFunctions(r.Context(), target, kept); err != nil {
		http.Error(w, "cannot delete function", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
