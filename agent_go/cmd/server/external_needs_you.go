package server

import (
	"context"
	"net/http"
	"sort"
	"strings"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
)

// "Needs you" over MCP: the live agent questions waiting on this person (a
// paused chat, run, scheduled run or function call) across workflows, Flows
// and Crews, and one way to answer them. Visibility follows the app
// (humanFeedbackVisibleTo).

func externalNeedsYouDefinitions(add func(string, string, bool, bool, map[string]any, ...string)) {
	filter := map[string]any{
		"workflow_id": externalString("Optional: only this workflow or Flow."),
		"crew_id":     externalString("Optional: only this Crew."),
	}
	add("list_needs_you", "List the questions agents are waiting on you to answer, across workflows, Flows and Crews: a paused chat, run, scheduled run or function call, with when each expires. Soonest first. Answer with needs_you action=answer.", false, false, filter)
	add("answer_needs_you", "Answer one question from needs_you action=list. Pass option (an option id or title) or answer (free text). The answer goes back to the paused chat or run, which continues. If someone already answered, says so.", true, false, map[string]any{
		"id":     externalString("Item id from needs_you action=list."),
		"option": externalString("Chosen option id or title, or your own choice when allow_free_text is true."),
		"answer": map[string]any{"type": "string", "maxLength": 8000, "description": "Free-text answer."},
	}, "id")
}

type needsYouPlace struct {
	Kind  string `json:"kind"` // workflow, relay or crew
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`
	path  string
}

// needsYouPlaces is every workflow, Flow and Crew this connection may read,
// narrowed by an optional filter.
func (api *StreamingAPI) needsYouPlaces(ctx context.Context, claims *UserClaims, visible []DiscoveredWorkflow, workflowID, crewID string) []needsYouPlace {
	places := []needsYouPlace{}
	tokenWorkflows := claims.AccessToken == nil || claims.AccessToken.Allows("workflows:read") || claims.AccessToken.Allows("runs:execute")
	if crewID == "" && tokenWorkflows {
		for _, item := range visible {
			if item.Manifest == nil || (workflowID != "" && item.Manifest.ID != workflowID) {
				continue
			}
			kind := "workflow"
			if item.Manifest.Kind == "relay" {
				kind = "relay"
			}
			places = append(places, needsYouPlace{Kind: kind, ID: item.Manifest.ID, Label: item.Manifest.Label, path: item.WorkspacePath})
		}
	}
	tokenCrews := claims.AccessToken == nil || claims.AccessToken.Allows("crews:read") || claims.AccessToken.Allows("crews:run")
	if workflowID == "" && tokenCrews && api.agentProfiles != nil {
		crews, _ := api.externalCrewsVisible(ctx, claims, "")
		for _, summary := range crews {
			id, _ := summary["id"].(string)
			if id == "" || (crewID != "" && id != crewID) || (claims.AccessToken != nil && !claims.AccessToken.AllowsCrew(id)) {
				continue
			}
			if crew, _, _, ok := api.externalCrewResolve(ctx, claims, id); ok {
				label, _ := summary["name"].(string)
				places = append(places, needsYouPlace{Kind: "crew", ID: id, Label: label, path: crew.Binding.WorkspacePath})
			}
		}
	}
	return places
}

func (api *StreamingAPI) externalNeedsYou(w http.ResponseWriter, r *http.Request, name string, args map[string]any, visible []DiscoveredWorkflow) {
	ctx := r.Context()
	claims := GetUserFromContext(ctx)
	if t := claims.AccessToken; t != nil && name != "list_needs_you" && !t.Allows("runs:execute") && !t.Allows("crews:run") && !t.Allows("builder:chat") {
		externalError(w, 403, "insufficient_scope", "This connection cannot answer questions.")
		return
	}
	places := api.needsYouPlaces(ctx, claims, visible, externalArg(args, "workflow_id"), externalArg(args, "crew_id"))
	filtered := externalArg(args, "workflow_id") != "" || externalArg(args, "crew_id") != ""
	api.needsYouRespond(w, r, name, args, claims, places, filtered)
}

// needsYouRespond lists or answers live questions for the given places;
// filtered is true when the places were narrowed by a filter.
func (api *StreamingAPI) needsYouRespond(w http.ResponseWriter, r *http.Request, name string, args map[string]any, claims *UserClaims, places []needsYouPlace, filtered bool) {
	questions := api.collectNeedsYou(r.Context(), claims, places, !filtered)
	if name == "list_needs_you" {
		items := []map[string]any{}
		sort.Slice(questions, func(i, j int) bool { return questions[i].request.ExpiresAt.Before(questions[j].request.ExpiresAt) })
		for _, q := range questions {
			item := map[string]any{"id": q.request.UniqueID, "type": "question", "question": q.request.MessageForUser,
				"options": q.request.Options, "allow_free_text": q.request.AllowFeedback || len(q.request.Options) == 0,
				"asked_at": q.request.CreatedAt, "expires_at": q.request.ExpiresAt}
			if q.request.Context != "" {
				item["context"] = q.request.Context
			}
			if q.place != nil {
				item["from"] = q.place
			}
			items = append(items, item)
		}
		truncated := len(items) > homeSectionLimit
		if truncated {
			items = items[:homeSectionLimit]
		}
		externalJSON(w, map[string]any{"items": items, "count": len(items), "truncated": truncated})
		return
	}

	id := externalArg(args, "id")
	option := strings.TrimSpace(externalArg(args, "option"))
	answer := strings.TrimSpace(externalArg(args, "answer"))
	for _, q := range questions {
		if q.request.UniqueID != id {
			continue
		}
		response := answer
		if option != "" {
			response = option
			if answer != "" {
				response = option + "\n\n" + answer
			}
		}
		if response == "" {
			externalError(w, 400, "invalid_arguments", "Pass option or answer.")
			return
		}
		if err := virtualtools.GetHumanFeedbackStore().SubmitResponse(id, response); err != nil {
			externalError(w, 409, "already_answered", "This question was already answered or has expired.")
			return
		}
		externalJSON(w, map[string]any{"id": id, "status": "answered", "type": "question"})
		return
	}
	externalError(w, 404, "not_found", "No open question with that id for you; it may be answered, expired, or not yours. Use needs_you action=list.")
}
