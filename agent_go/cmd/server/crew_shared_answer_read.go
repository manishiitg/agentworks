package server

import (
	"net/http"
	"time"
)

// Team ask adapters return a normal mailbox receipt. Reading the answer uses
// that same stored caller identity and mailbox admission, never the maker's
// old ask thread. Non-Team publication reads keep their existing behavior.
func (api *StreamingAPI) externalTeamAnswerRead(w http.ResponseWriter, r *http.Request, action string, args map[string]any, project, owner, key string) bool {
	if action != "read_chat" || IsLocalMode() {
		return false
	}
	claims := GetUserFromContext(r.Context())
	team, err := readCrewTeam(project)
	if err != nil || !crewTeamAgentsVisible(team, claims.UserID) || claims.UserID == owner {
		return false
	}
	agent, ok := team.registeredAgent(owner, key)
	if !ok || !agent.InTeam {
		externalError(w, 403, "agent_unavailable", "This agent is no longer In team.")
		return true
	}
	inbox := externalArg(args, "inbox_id")
	if inbox == "" {
		externalError(w, 400, "answer_inbox_required", "Read the answer with messages action=read using the inbox_id returned by ask.")
		return true
	}
	caller, err := api.externalMessageCaller(r.Context(), claims, args)
	if err != nil {
		externalCommunicationError(w, 400, "message_refused", err)
		return true
	}
	out, err := api.readAgentMessages(r.Context(), claims.UserID, caller, inbox, externalInt(args, "after", 0), 100, time.Duration(externalInt(args, "wait_seconds", 0))*time.Second)
	if err != nil {
		externalError(w, 403, "conversation_unavailable", "This answer inbox is unavailable to this caller.")
		return true
	}
	externalJSON(w, out)
	return true
}
