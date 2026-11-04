package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// Same sender-thread contract as the Mac. The publication changes execution
// location, never the audience or which sender may read a thread.
func (api *StreamingAPI) publishedAgentCall(ctx context.Context, owner, project string, call crewChatCall) crewChatResult {
	fail := func(status int, message string) crewChatResult {
		body, _ := json.Marshal(map[string]string{"error": message})
		return crewChatResult{ID: call.ID, Status: status, Body: body}
	}
	if IsLocalMode() || owner == "" || ownerOfCrewProjectID(project) != owner {
		return fail(403, "This agent is unavailable.")
	}
	place, err := crewPublishedAgent(project, call.Tab)
	if err != nil || place == nil || place.Location != "server" {
		return fail(409, "This agent is not ready on the server yet.")
	}
	var args struct {
		SenderID string `json:"sender_id"`
	}
	if json.Unmarshal(call.Args, &args) != nil || args.SenderID == "" {
		return fail(403, "This agent is unavailable.")
	}
	if _, reason := macSharedAgentFor(project, call.Tab, args.SenderID, owner); reason != "" {
		return fail(403, "This agent is no longer shared with you.")
	}
	if call.Operation != "agent_ask" && call.Operation != "agent_read" && call.Operation != "agent_answer" {
		return fail(400, "Send a message, read its reply or answer its question.")
	}
	claims := &UserClaims{UserID: owner, Username: owner}
	ctx = context.WithValue(ctx, UserContextKey, claims)
	profile, err := api.agentProfiles.Resolve(crewProfileID, 0, owner)
	if err != nil {
		return fail(409, "This agent is unavailable.")
	}
	return api.desktopMacSharedAgent(ctx, claims, profile, project, call)
}

func crewPublishedTarget(project, target string) bool {
	tab := macMainTab
	if target != "" && target != "main" && target != project {
		if strings.HasPrefix(target, project+codeChatSideMarker) {
			tab = crewAgentPublishTab(target)
		} else {
			tab = target
		}
	}
	entry, err := crewPublishedAgent(project, tab)
	return err == nil && entry != nil && entry.Location == "server"
}

// Called only after the existing source-owner check. This reuses the owner's
// normal workspace conversation adapter; it does not impersonate a sender.
func (api *StreamingAPI) publishedOwnerWorkspace(ctx context.Context, owner, project, caller, action string, args map[string]any) crewChatResult {
	claims := &UserClaims{UserID: owner, Username: owner}
	ctx = context.WithValue(ctx, UserContextKey, claims)
	raw, _ := json.Marshal(args)
	return api.desktopCrewWorkspace(ctx, claims, project, crewChatCall{Operation: "workspace", Action: action, Args: raw, Caller: caller})
}

// A reach-off request must not delete a Crew containing a published runtime.
func crewHasPublishedAgents(project string) bool {
	entries, err := readCrewAgentPlacements(project)
	return err != nil || len(entries) > 0
}

func crewPublishedOwnerRoute(r *http.Request, project string, args map[string]any) bool {
	if IsLocalMode() || GetUserIDFromContext(r.Context()) != ownerOfCrewProjectID(project) {
		return false
	}
	b, err := crewroot.ReadSync(project)
	return err == nil && b != nil && b.Side == "server" && crewPublishedTarget(project, externalArg(args, "chat_id"))
}
