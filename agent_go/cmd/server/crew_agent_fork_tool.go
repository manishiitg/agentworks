package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"

	"github.com/gorilla/mux"
)

// Chat and MCP call exactly the screen's handlers. A chat supplies its own
// session-derived source ID; MCP may select only an agent the caller owns.
func (api *StreamingAPI) crewAgentForkTool(r *http.Request, claims *UserClaims, project, action string, args map[string]any) (string, error) {
	if !crewTeamClaims(claims, project, action != "forks") {
		return "", fmt.Errorf("use your own project account with permission for this action")
	}
	r = r.WithContext(context.WithValue(r.Context(), UserContextKey, claims))
	operation := map[string]string{"fork_from": "self", "fork_send": "send", "fork_accept": "accept", "fork_decline": "cancel", "fork_cancel": "cancel"}[action]
	if action != "forks" && operation == "" {
		return "", fmt.Errorf("choose a fork action")
	}
	input := map[string]any{"agent_id": externalArg(args, "agent_key"), "recipient_id": externalArg(args, "person"), "fork_id": externalArg(args, "fork_id")}
	path, method := "/api/crew-teams/"+url.PathEscape(project)+"/agent-forks", http.MethodPost
	vars := map[string]string{"project": project}
	handler := api.handleCrewAgentFork
	if IsLocalMode() {
		if ownerOfCrewProjectID(project) != claims.UserID {
			return "", fmt.Errorf("open your own copy of this project")
		}
		if operation == "self" {
			path = "/api/crew-teams/" + url.PathEscape(project) + "/agent-fork-local?self=1"
			handler = api.handleCrewAgentSelfForkLocal
		} else if operation == "send" || operation == "accept" {
			path = "/api/crew-teams/" + url.PathEscape(project) + "/agent-fork-local"
			input["operation"] = operation
			handler = api.handleCrewAgentForkLocal
		} else {
			_, binding, connection, err := api.desktopCrewChatBinding(r.Context(), project)
			if err != nil {
				return "", err
			}
			route := "/api/crew-teams/" + url.PathEscape(binding.PeerProjectID) + "/agent-forks"
			if action == "forks" {
				method = http.MethodGet
			} else {
				route += "/" + url.PathEscape(externalArg(args, "fork_id")) + "/cancel"
			}
			var result json.RawMessage
			if err = crewTeamRemoteJSON(r.Context(), claims.UserID, connection, method, route, nil, &result); err != nil {
				return "", err
			}
			return string(result), nil
		}
	} else if operation == "self" {
		return "", fmt.Errorf("Fork from your own connected Mac using its project.")
	} else if action == "forks" {
		method = http.MethodGet
	} else if operation != "send" {
		vars["fork"], vars["op"] = externalArg(args, "fork_id"), operation
		path += "/" + url.PathEscape(vars["fork"]) + "/" + operation
	}
	body, err := json.Marshal(input)
	if err != nil {
		return "", err
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(body)).WithContext(r.Context())
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler(response, mux.SetURLVars(request, vars))
	if response.Code >= 300 {
		return "", fmt.Errorf("%s", laptopActionErrorText(response.Body.String()))
	}
	return response.Body.String(), nil
}
