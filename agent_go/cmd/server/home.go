package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productpolicy"
)

const homeWorkspaceLimit = 200
const homeSectionLimit = 100

type homeWork struct {
	Surface       string `json:"surface"`
	ID            string `json:"id"`
	Label         string `json:"label"`
	WorkspacePath string `json:"workspace_path"`
	UpdatedAt     string `json:"updated_at,omitempty"`
}
type homeAttention struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Title     string    `json:"title"`
	Work      *homeWork `json:"work,omitempty"`
	SessionID string    `json:"session_id,omitempty"`
	View      string    `json:"view,omitempty"`
	UpdatedAt string    `json:"updated_at,omitempty"`
}
type homeResponse struct {
	Attention   []homeAttention      `json:"attention"`
	Work        []homeWork           `json:"work"`
	Running     []*ActiveSessionInfo `json:"running"`
	RunningWork map[string]homeWork  `json:"running_work"`
	Warnings    []string             `json:"warnings"`
	Truncated   bool                 `json:"truncated"`
}

func homeProductAllowed(claims *UserClaims, surface string) bool {
	return productpolicy.Enabled(surface) && userAllowedProduct(claims, surface) && !userAccessForClaims(claims).Disabled
}

// The same inventories as product links and MCP. Never accept a workspace path
// from the browser as authority to discover another person's work.
func (api *StreamingAPI) homeInventory(ctx context.Context) ([]homeWork, error) {
	claims := GetUserFromContext(ctx)
	discovered, err := DiscoverWorkflowManifests(ctx)
	if err != nil {
		return nil, err
	}
	out := []homeWork{}
	for _, item := range filterWorkflowManifestsForUser(claims, discovered) {
		if item.Manifest == nil {
			continue
		}
		surface := "agentworks"
		if item.Manifest.Kind == "relay" {
			surface = "relays"
		}
		if !homeProductAllowed(claims, surface) {
			continue
		}
		if claims.AccessToken != nil && (!claims.AccessToken.AllowsWorkflow(item.Manifest.ID) || !claims.AccessToken.Allows("workflows:read")) {
			continue
		}
		out = append(out, homeWork{Surface: surface, ID: item.Manifest.ID, Label: item.Manifest.Label, WorkspacePath: item.WorkspacePath, UpdatedAt: item.Manifest.UpdatedAt})
	}
	if api.agentProfiles != nil {
		if homeProductAllowed(claims, "work") && (claims.AccessToken == nil || claims.AccessToken.Allows("crews:read")) {
			crews, err := api.externalCrewsVisible(ctx, claims, "")
			if err != nil {
				return nil, err
			}
			for _, row := range crews {
				out = append(out, homeWork{Surface: "work", ID: fmt.Sprint(row["id"]), Label: fmt.Sprint(row["name"]), WorkspacePath: fmt.Sprint(row["workspace_path"])})
			}
		}
		// Code is private to its owner; scoped external tokens have no Code scope.
		if homeProductAllowed(claims, "code") && claims.AccessToken == nil {
			if profile, err := api.agentProfiles.Resolve("code", 0, claims.UserID); err == nil {
				for _, row := range listProjectsForOwner(ctx, claims, profile, claims.UserID, false) {
					out = append(out, homeWork{Surface: "code", ID: row.ID, Label: row.Title, WorkspacePath: row.WorkspacePath, UpdatedAt: row.UpdatedAt})
				}
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i].Label) < strings.ToLower(out[j].Label) })
	return out, nil
}

func homeDecisionNeedsAttention(input ReportHumanInput) bool {
	return input.Status == "pending" || (input.Status == "answered" && strings.TrimSpace(input.ApplyMessage) != "")
}

func homeSessionRunning(session *ActiveSessionInfo) bool {
	if session == nil || session.ParentSessionID != "" {
		return false
	}
	if state := session.RuntimeState; state != nil {
		return runtimePhaseIsLive(state.Phase) || (state.BackgroundLive && state.Phase != runtimePhaseFailed && state.Phase != runtimePhaseCompleted && state.Phase != runtimePhaseCanceled)
	}
	return session.Status == "running" || session.Status == "paused" || session.NeedsUserInput || session.HasRunningBackgroundAgents
}

func (api *StreamingAPI) collectHome(r *http.Request) (homeResponse, error) {
	start := time.Now()
	result := homeResponse{Attention: []homeAttention{}, Work: []homeWork{}, Running: []*ActiveSessionInfo{}, RunningWork: map[string]homeWork{}, Warnings: []string{}}
	defer func() {
		log.Printf("[HOME] user=%s attention=%d work=%d running=%d ms=%d", GetUserIDFromContext(r.Context()), len(result.Attention), len(result.Work), len(result.Running), time.Since(start).Milliseconds())
	}()
	work, err := api.homeInventory(r.Context())
	if err != nil {
		return result, err
	}
	if state, stateErr := readWorkspaceReturnState(r); stateErr == nil {
		rank := map[string]int{}
		for i, d := range state.Recent {
			rank[d.Surface+":"+d.ID] = i + 1
		}
		sort.SliceStable(work, func(i, j int) bool {
			left, right := rank[work[i].Surface+":"+work[i].ID], rank[work[j].Surface+":"+work[j].ID]
			if left != 0 || right != 0 {
				if left == 0 {
					return false
				}
				if right == 0 {
					return true
				}
				return left < right
			}
			return work[i].UpdatedAt > work[j].UpdatedAt
		})
	}
	if len(work) > homeWorkspaceLimit {
		work = work[:homeWorkspaceLimit]
		result.Truncated = true
		result.Warnings = append(result.Warnings, "Home shows the first 200 workspaces. Use product navigation to browse all work.")
	}
	result.Work = work
	byPath := map[string]*homeWork{}
	for i := range work {
		byPath[canonicalChatHistoryWorkspacePath(GetUserIDFromContext(r.Context()), work[i].WorkspacePath)] = &work[i]
	}
	for _, q := range api.visibleHumanFeedback(r.Context(), virtualtools.GetHumanFeedbackStore().ListPending(time.Now())) {
		active, ok := api.getActiveSession(q.SessionID)
		if !ok {
			continue
		}
		place := byPath[canonicalChatHistoryWorkspacePath(GetUserIDFromContext(r.Context()), active.WorkspacePath)]
		if place == nil {
			continue
		} // includes product and token bounds
		result.Attention = append(result.Attention, homeAttention{ID: q.UniqueID, Kind: "question", Title: q.MessageForUser, Work: place, SessionID: q.SessionID, UpdatedAt: q.CreatedAt.Format(time.RFC3339)})
	}
	for i := range work {
		place := &work[i]
		if read, _ := reportHumanInputAccess(r.Context(), place.WorkspacePath); !read {
			continue
		}
		inputs, err := listReportHumanInputsBounded(r.Context(), place.WorkspacePath, "needs-attention", "", homeSectionLimit+1)
		if err != nil {
			result.Warnings = append(result.Warnings, "Some decisions could not be loaded.")
			continue
		}
		if len(inputs) > homeSectionLimit {
			inputs = inputs[:homeSectionLimit]
			result.Truncated = true
		}
		withDecisionApplyMessages(inputs)
		for _, input := range inputs {
			if !homeDecisionNeedsAttention(input) {
				continue
			}
			kind := "decision"
			if input.Status == "answered" {
				kind = "apply_decision"
			}
			view := "human-actions"
			if place.Surface == "work" || place.Surface == "code" {
				view = "suggestions"
			}
			result.Attention = append(result.Attention, homeAttention{ID: input.ID, Kind: kind, Title: input.Question, Work: place, View: view, UpdatedAt: input.UpdatedAt})
		}
		if place.Surface != "agentworks" && place.Surface != "relays" {
			continue
		}
		updates, err := services.ListOrgDashboardNotifications(r.Context(), place.WorkspacePath, 1)
		if err != nil {
			result.Warnings = append(result.Warnings, "Some run summaries could not be loaded.")
			continue
		}
		// Only the latest run summary: a later success clears an old failure.
		if latest := updates.RunSummary; latest != nil && (latest.Status == "failed" || latest.Status == "blocked" || latest.Status == "waiting_for_platform" || latest.Status == "no_run") {
			result.Attention = append(result.Attention, homeAttention{ID: latest.ID, Kind: latest.Status, Title: firstNonEmptyTrimmed(latest.Title, latest.Message), Work: place, View: "execution-logs", UpdatedAt: latest.CreatedAt})
		}
	}
	sort.SliceStable(result.Attention, func(i, j int) bool {
		if (result.Attention[i].Kind == "question") != (result.Attention[j].Kind == "question") {
			return result.Attention[i].Kind == "question"
		}
		return result.Attention[i].UpdatedAt > result.Attention[j].UpdatedAt
	})
	if len(result.Attention) > homeSectionLimit {
		result.Attention = result.Attention[:homeSectionLimit]
		result.Truncated = true
	}
	for _, session := range api.collectActiveSessions(r.Context()) {
		if homeSessionRunning(session) && byPath[canonicalChatHistoryWorkspacePath(GetUserIDFromContext(r.Context()), session.WorkspacePath)] != nil {
			result.Running = append(result.Running, session)
			result.RunningWork[session.SessionID] = *byPath[canonicalChatHistoryWorkspacePath(GetUserIDFromContext(r.Context()), session.WorkspacePath)]
		}
	}
	if len(result.Running) > homeSectionLimit {
		result.Running = result.Running[:homeSectionLimit]
		result.Truncated = true
	}
	// Keep partial-data messages useful without repeating one per workspace.
	seen := map[string]bool{}
	warnings := []string{}
	for _, warning := range result.Warnings {
		if !seen[warning] {
			warnings = append(warnings, warning)
			seen[warning] = true
		}
	}
	result.Warnings = warnings
	return result, nil
}

func (api *StreamingAPI) handleHome(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	if claims == nil || claims.UserID == "" {
		http.Error(w, "unauthenticated", 401)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	result, err := api.collectHome(r.WithContext(ctx))
	if err != nil {
		http.Error(w, "Could not load your work. Please try again.", 503)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}
