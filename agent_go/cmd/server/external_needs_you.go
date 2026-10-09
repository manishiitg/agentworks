package server

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"time"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
)

// "Needs you" over MCP: one list of what waits on this person across
// workflows, Relays and Crews, and one way to answer it. Two sources:
// live agent questions (a paused chat, run, scheduled run or function call)
// and stored decisions and suggestions. Visibility and answering follow
// the app: questions through humanFeedbackVisibleTo, decisions through the
// workspace read/write check.

func externalNeedsYouDefinitions(add func(string, string, bool, bool, map[string]any, ...string)) {
	filter := map[string]any{
		"workflow_id": externalString("Optional: only this workflow or Relay."),
		"crew_id":     externalString("Optional: only this Crew."),
	}
	add("list_needs_you", "List what is waiting on you across workflows, Relays and Crews: live agent questions (a paused chat, run, scheduled run or function call, with when it expires) and open decisions (with Pulse's recommendation when there is one) and suggestions sent to you as owner. Urgent first. Answer with answer_needs_you.", false, false, filter)
	add("answer_needs_you", "Answer or dismiss one item from list_needs_you. Pass option (an option id or title) or answer (free text), or dismiss=true for a decision. A live question goes back to the paused chat or run, which continues; a decision is recorded as answered by you via MCP and applied by the Builder as from the app. If someone already answered, says so.", true, false, map[string]any{
		"id":      externalString("Item id from needs_you action=list."),
		"option":  externalString("Chosen option id or title."),
		"answer":  map[string]any{"type": "string", "maxLength": 8000, "description": "Free-text answer or note."},
		"dismiss": map[string]any{"type": "boolean", "description": "Dismiss a decision without answering."},
	}, "id")
}

type needsYouPlace struct {
	Kind  string `json:"kind"` // workflow, relay or crew
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`
	path  string
}

// needsYouPlaces is every workflow, Relay and Crew this connection may read,
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
	if t := claims.AccessToken; t != nil && name == "answer_needs_you" && !t.Allows("runs:execute") && !t.Allows("crews:run") && !t.Allows("builder:chat") {
		externalError(w, 403, "insufficient_scope", "This connection cannot answer questions.")
		return
	}
	places := api.needsYouPlaces(ctx, claims, visible, externalArg(args, "workflow_id"), externalArg(args, "crew_id"))
	byPath := map[string]needsYouPlace{}
	for _, place := range places {
		byPath[strings.Trim(place.path, "/")] = place
	}
	filtered := externalArg(args, "workflow_id") != "" || externalArg(args, "crew_id") != ""

	// Live agent questions this person may answer.
	type question struct {
		request virtualtools.HumanFeedbackRequest
		place   *needsYouPlace
	}
	questions := []question{}
	for _, request := range api.visibleHumanFeedback(ctx, virtualtools.GetHumanFeedbackStore().ListPending(time.Now())) {
		api.activeSessionsMux.RLock()
		session := api.activeSessions[request.SessionID]
		workspace := ""
		if session != nil {
			workspace = strings.Trim(session.WorkspacePath, "/")
		}
		api.activeSessionsMux.RUnlock()
		var place *needsYouPlace
		if found, ok := byPath[workspace]; ok {
			place = &found
		}
		// A scoped connection sees only questions from places it may reach.
		if (filtered || claims.AccessToken != nil) && place == nil {
			continue
		}
		questions = append(questions, question{request: request, place: place})
	}

	// Open decisions and suggestions.
	type decision struct {
		input ReportHumanInput
		place needsYouPlace
	}
	decisions := []decision{}
	for _, place := range places {
		if read, _ := reportHumanInputAccess(ctx, place.path); !read {
			continue
		}
		inputs, err := listReportHumanInputs(ctx, place.path, "pending", "")
		if err != nil {
			continue
		}
		for _, input := range inputs {
			decisions = append(decisions, decision{input: input, place: place})
		}
	}

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
		priority := map[string]int{"high": 0, "medium": 1, "low": 2}
		sort.SliceStable(decisions, func(i, j int) bool {
			return priority[decisions[i].input.Priority] < priority[decisions[j].input.Priority]
		})
		for _, d := range decisions {
			kind := "decision"
			if d.input.Source == "user_suggestion" {
				kind = "suggestion"
			}
			item := map[string]any{"id": d.input.ID, "type": kind, "question": d.input.Question, "options": d.input.Options,
				"allow_free_text": d.input.AllowFreeText, "priority": d.input.Priority, "source": d.input.Source,
				"asked_at": d.input.CreatedAt, "from": d.place}
			if d.input.Context != "" {
				item["context"] = d.input.Context
			}
			if d.input.Recommendation != nil {
				item["pulse_recommends"] = d.input.Recommendation
			}
			items = append(items, item)
		}
		externalJSON(w, map[string]any{"items": items, "count": len(items)})
		return
	}

	id := externalArg(args, "id")
	option := strings.TrimSpace(externalArg(args, "option"))
	answer := strings.TrimSpace(externalArg(args, "answer"))
	dismiss, _ := args["dismiss"].(bool)
	for _, q := range questions {
		if q.request.UniqueID != id {
			continue
		}
		if dismiss {
			externalError(w, 400, "invalid_arguments", "A live question cannot be dismissed; answer it, or let it expire.")
			return
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
	for _, d := range decisions {
		if d.input.ID != id {
			continue
		}
		if _, write := reportHumanInputAccess(ctx, d.place.path); !write {
			externalError(w, 403, "forbidden", "Answering this needs write access to "+d.place.Kind+" "+d.place.ID+".")
			return
		}
		req := ReportHumanInputAnswerRequest{WorkspacePath: d.place.path, Note: answer, AnsweredBy: claims.UserID, AnsweredByKind: "human_via_chat", AnsweredVia: "mcp"}
		if option != "" {
			for _, choice := range d.input.Options {
				if choice.ID == option || strings.EqualFold(choice.Title, option) {
					req.SelectedOptionID = choice.ID
				}
			}
			if req.SelectedOptionID == "" {
				externalError(w, 400, "invalid_arguments", "Unknown option; use an option id or title from needs_you action=list.")
				return
			}
		}
		var (
			input *ReportHumanInput
			err   error
		)
		if dismiss {
			input, err = dismissReportHumanInput(ctx, d.place.path, id, req)
		} else {
			if req.SelectedOptionID == "" && req.Note == "" {
				externalError(w, 400, "invalid_arguments", "Pass option or answer, or dismiss=true.")
				return
			}
			input, err = answerReportHumanInput(ctx, d.place.path, id, req)
		}
		if err != nil {
			externalError(w, 409, "not_answered", err.Error())
			return
		}
		out := map[string]any{"id": id, "type": "decision", "status": input.Status}
		if !dismiss && input != nil {
			if message := decisionApplyChatMessage(*input); message != "" {
				out["apply_message"] = message
				out["next"] = "To apply it now, send apply_message to the workflow's Builder with builder action=chat; otherwise the Builder or Pulse applies it on its next turn."
			}
		}
		externalJSON(w, out)
		return
	}
	externalError(w, 404, "not_found", "No open item with that id for you; it may be answered, expired, or not yours. Use needs_you action=list.")
}
