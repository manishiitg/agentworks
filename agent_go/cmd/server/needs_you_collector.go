package server

import (
	"context"
	"time"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
)

// needsYouPlace is one workflow, Flow or Crew whose waiting questions Home shows.
type needsYouPlace struct {
	Kind  string `json:"kind"` // workflow, relay or crew
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`
	path  string
}

type needsYouQuestion struct {
	request virtualtools.HumanFeedbackRequest
	place   *needsYouPlace
}

// collectNeedsYou returns the live questions waiting on the person: a paused
// chat, run, scheduled run or function call, for Home. Places
// come only from mediated inventories. Unbound questions remain visible to the
// original unfiltered browser-session MCP call; scoped tokens never get them.
func (api *StreamingAPI) collectNeedsYou(ctx context.Context, claims *UserClaims, places []needsYouPlace, includeUnbound bool) []needsYouQuestion {
	questions := []needsYouQuestion{}
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
		questions = append(questions, needsYouQuestion{request: request, place: place})
	}
	return questions
}
