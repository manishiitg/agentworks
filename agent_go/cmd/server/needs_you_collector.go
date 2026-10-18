package server

import (
	"context"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
)

type needsYouQuestion struct {
	request virtualtools.HumanFeedbackRequest
	place   *needsYouPlace
}
type needsYouDecision struct {
	input ReportHumanInput
	place needsYouPlace
}
type needsYouFailure struct {
	ID, Status, Title, UpdatedAt string
	Place                        needsYouPlace
}
type needsYouCollection struct {
	failures  []needsYouFailure
	questions []needsYouQuestion
	decisions []needsYouDecision
	warnings  []string
	truncated bool
}

// Home and MCP share the same live-question and durable-decision projection.
// Places come only from mediated inventories. Unbound questions remain visible
// to the original unfiltered browser-session MCP call; scoped tokens never get
// them. Answer lookup keeps its original unbounded semantics.
func (api *StreamingAPI) collectNeedsYou(ctx context.Context, claims *UserClaims, places []needsYouPlace, includeUnbound bool, perWorkspaceLimit int) needsYouCollection {
	result := needsYouCollection{questions: []needsYouQuestion{}, decisions: []needsYouDecision{}, warnings: []string{}}
	byPath := map[string]needsYouPlace{}
	for _, place := range places {
		byPath[canonicalChatHistoryWorkspacePath(claims.UserID, place.path)] = place
	}
	for _, request := range api.visibleHumanFeedback(ctx, virtualtools.GetHumanFeedbackStore().ListPending(time.Now())) {
		session, ok := api.getActiveSession(request.SessionID)
		if !ok {
			continue
		}
		var place *needsYouPlace
		if found, ok := byPath[canonicalChatHistoryWorkspacePath(claims.UserID, session.WorkspacePath)]; ok {
			place = &found
		}
		if place == nil && (!includeUnbound || claims.AccessToken != nil) {
			continue
		}
		result.questions = append(result.questions, needsYouQuestion{request: request, place: place})
	}
	seen := map[string]bool{}
	for _, place := range places {
		canonical := canonicalChatHistoryWorkspacePath(claims.UserID, place.path)
		if seen[canonical] {
			continue
		}
		seen[canonical] = true
		if read, _ := reportHumanInputAccess(ctx, place.path); !read {
			continue
		}
		limit := 0
		if perWorkspaceLimit > 0 {
			limit = perWorkspaceLimit + 1
		}
		inputs, err := listReportHumanInputsBounded(ctx, place.path, "needs-attention", "", limit)
		if err != nil {
			result.warnings = append(result.warnings, "Some decisions could not be loaded.")
			continue
		}
		if perWorkspaceLimit > 0 && len(inputs) > perWorkspaceLimit {
			inputs = inputs[:perWorkspaceLimit]
			result.truncated = true
		}
		withDecisionApplyMessages(inputs)
		for _, input := range inputs {
			if homeDecisionNeedsAttention(input) {
				result.decisions = append(result.decisions, needsYouDecision{input: input, place: place})
			}
		}
	}
	if perWorkspaceLimit > 0 {
		for _, place := range places {
			if place.Kind != "workflow" && place.Kind != "relay" {
				continue
			}
			if read, _ := reportHumanInputAccess(ctx, place.path); !read {
				continue
			}
			updates, err := services.ListOrgDashboardNotifications(ctx, place.path, 1)
			if err != nil {
				result.warnings = append(result.warnings, "Some run summaries could not be loaded.")
				continue
			}
			if latest := updates.RunSummary; latest != nil && (latest.Status == "failed" || latest.Status == "blocked" || latest.Status == "waiting_for_platform" || latest.Status == "no_run") {
				result.failures = append(result.failures, needsYouFailure{ID: latest.ID, Status: latest.Status, Title: firstNonEmptyTrimmed(latest.Title, latest.Message), UpdatedAt: latest.CreatedAt, Place: place})
			}
		}
	}
	return result
}

func homeDecisionNeedsAttention(input ReportHumanInput) bool {
	return input.Status == "pending" || (input.Status == "answered" && input.ApplyMessage != "")
}
