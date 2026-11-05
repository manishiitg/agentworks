package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// Same sender-thread contract as the Mac. The publication changes execution
// location, never the audience or which sender may read a thread.
// byAgent is set by the server for an agent message (AgentBridge), never read from the call's arguments: a person's
// own ask cannot claim it to reach a public agent outside their audience.
func (api *StreamingAPI) publishedAgentCall(ctx context.Context, owner, project string, call crewChatCall, byAgent bool) crewChatResult {
	return api.serverAgentCall(ctx, owner, project, call, byAgent, false)
}

// standbyAgentCall runs the call on the server from the agent's standby copy (Always on, crew_agent_always_on.go):
// the caller routed it there with crewStandbyRoute.
func (api *StreamingAPI) standbyAgentCall(ctx context.Context, owner, project string, call crewChatCall, byAgent bool) crewChatResult {
	started := time.Now()
	result := api.serverAgentCall(ctx, owner, project, call, byAgent, true)
	if call.Operation == "agent_ask" {
		crewAlwaysOnLog(project, call.Tab, "answered_on_server", fmt.Sprintf("status_%d", result.Status), started)
	}
	return result
}

// serverAgentCall runs a Mac agent's call on this server: a published agent (its placement), or one answering from
// its standby copy while its owner's Mac is off. Either way as the owner's agent, with the real asker's identity.
func (api *StreamingAPI) serverAgentCall(ctx context.Context, owner, project string, call crewChatCall, byAgent, standby bool) crewChatResult {
	fail := func(status int, message string) crewChatResult {
		body, _ := json.Marshal(map[string]string{"error": message})
		return crewChatResult{ID: call.ID, Status: status, Body: body}
	}
	if IsLocalMode() || owner == "" || ownerOfCrewProjectID(project) != owner {
		return fail(403, "This agent is unavailable.")
	}
	if !standby {
		place, err := crewPublishedAgent(project, call.Tab)
		if err != nil || place == nil || place.Location != "server" {
			return fail(409, "This agent is not ready on the server yet.")
		}
	}
	var args struct {
		SenderID string `json:"sender_id"`
	}
	if json.Unmarshal(call.Args, &args) != nil || args.SenderID == "" {
		return fail(403, "This agent is unavailable.")
	}
	// An agent message may also reach a public agent; a person's own ask needs the audience.
	if _, reason := macSharedAgentAccess(project, call.Tab, args.SenderID, owner, byAgent); reason != "" {
		return fail(403, "This agent is no longer shared with you.")
	}
	if call.Operation != "agent_ask" && call.Operation != "agent_read" && call.Operation != "agent_answer" {
		return fail(400, "Send a message, read its reply or answer its question.")
	}
	if standby && crewStandbyRunForTest != nil {
		return crewStandbyRunForTest(ctx, project, call)
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
