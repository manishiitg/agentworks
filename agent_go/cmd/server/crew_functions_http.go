package server

import (
	"encoding/json"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/codeproduct"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gorilla/mux"
)

// Crew functions in the Automation panel (PLAT-357): the Functions tab lists
// a Crew's declared functions plus the implicit ask, and the recent calls
// made to them while the server has been running. Deletion is limited to the
// Crew owner, like triggers (projectManifest resolves the caller's own
// projects only).

const crewFunctionRecentCallsLimit = 25

func (s *ProductScheduleService) crewFunctionTarget(r *http.Request) (triggerTarget, string, int, string) {
	profileID, projectID := productWebhookCoordinates(r)
	if profileID == "" {
		profileID = "work"
	}
	userID := productWorkspaceUserID(r.Context())
	_, binding, _, err := s.projectManifest(r.Context(), userID, profileID, projectID)
	if err != nil {
		return triggerTarget{}, "", http.StatusNotFound, err.Error()
	}
	target := triggerTarget{
		Kind: triggerCallerCrew, Path: agentProfileRuntimeWorkspace(userID, binding.WorkspacePath),
		CrewID: projectID, CrewProfile: profileID, CrewOwner: userID,
	}
	return target, userID, 0, ""
}

type crewFunctionCallView struct {
	CallID         string                 `json:"call_id"`
	Function       string                 `json:"function"`
	CallerKind     string                 `json:"caller_kind"`
	CallerLabel    string                 `json:"caller_label"`
	Status         string                 `json:"status"`
	LatestProgress *crewFunctionProgress  `json:"latest_progress,omitempty"`
	Progress       []crewFunctionProgress `json:"progress,omitempty"`
	Result         interface{}            `json:"result,omitempty"`
	Error          string                 `json:"error,omitempty"`
	StartedAt      time.Time              `json:"started_at"`
	FinishedAt     *time.Time             `json:"finished_at,omitempty"`
}

// recentCrewFunctionCalls returns the latest calls made to one Crew. A call
// to a Code (a private same-owner peer call) is shown only to the person who
// made it: the Code's owner never sees an editor's private runs (#246 review).
func recentCrewFunctionCalls(targetID, requesterID string) []crewFunctionCallView {
	crewFunctionCalls.Lock()
	calls := make([]*crewFunctionCall, 0, len(crewFunctionCalls.m))
	for _, call := range crewFunctionCalls.m {
		calls = append(calls, call)
	}
	crewFunctionCalls.Unlock()
	views := []crewFunctionCallView{}
	for _, call := range calls {
		call.mu.Lock()
		if call.TargetKind != triggerCallerCrew || call.TargetID != strings.TrimSpace(targetID) {
			call.mu.Unlock()
			continue
		}
		if call.TargetProfileID == codeproduct.ProfileID && call.UserID != strings.TrimSpace(requesterID) {
			call.mu.Unlock()
			continue
		}
		view := crewFunctionCallView{
			CallID: call.ID, Function: call.Function, CallerKind: call.CallerKind, CallerLabel: call.CallerLabel,
			Status: call.Status, Result: call.Result, Error: call.Error, StartedAt: call.CreatedAt,
			Progress: append([]crewFunctionProgress(nil), call.Progress...),
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

func (s *ProductScheduleService) listCrewFunctionsHTTP(w http.ResponseWriter, r *http.Request) {
	target, _, status, message := s.crewFunctionTarget(r)
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
	for _, fn := range withDefaultAskFunction(functions) {
		_, declared := findCrewFunction(functions, fn.Name)
		views = append(views, functionView{crewFunction: fn, Implicit: !declared})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"functions": views, "calls": recentCrewFunctionCalls(target.CrewID, GetUserIDFromContext(r.Context()))})
}

func (s *ProductScheduleService) deleteCrewFunctionHTTP(w http.ResponseWriter, r *http.Request) {
	target, _, status, message := s.crewFunctionTarget(r)
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
		if name == crewFunctionAskName {
			http.Error(w, "the default ask function is built in and cannot be removed", http.StatusBadRequest)
			return
		}
		http.Error(w, "function not found", http.StatusNotFound)
		return
	}
	if err := writeCrewFunctions(r.Context(), target, kept); err != nil {
		http.Error(w, "cannot delete function", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
