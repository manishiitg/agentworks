package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productpolicy"
)

// Sharing an agent with everyone on this server is a person's choice made from
// their own CLI login: the in-chat command, or the terminal. It is not an MCP
// tool and an agent-bound connection cannot reach it. Sharing with one named
// person stays a browser action.

// serverSharesOf returns the agent's live server-scope shares.
func (s agentMessageStore) serverSharesOf(agentID string, now time.Time) []int {
	var idx []int
	for i, sh := range s.Shares {
		if sh.AgentID == agentID && sh.serverScope() && sh.live(now) {
			idx = append(idx, i)
		}
	}
	return idx
}

// revokeServerShares ends every server-scope share of an agent, bumping its
// revocation version so open conversations fail their next check, and suppresses
// mail that was never admitted. It returns how many it ended.
func (s *agentMessageStore) revokeServerShares(agentID, actor string, now time.Time) int {
	n := 0
	for _, i := range s.serverSharesOf(agentID, now) {
		sh := &s.Shares[i]
		sh.RevokedAt = now
		sh.RevocationVersion++
		s.suppressShare(sh.ID)
		s.auditShare("share_revoked", actor, sh.OwnerUserID, sh.SubjectUserID, sh.ID, sh.AgentID)
		n++
	}
	return n
}

func (api *StreamingAPI) handleAgentSharing(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer "+cliOAuthAccessPrefix) {
		externalError(w, http.StatusForbidden, "sharing_refused", "Share through your signed-in AgentWorks CLI, not an agent tool.")
		return
	}
	claims := GetUserFromContext(r.Context())
	if !productpolicy.Enabled("bullpen") {
		externalError(w, http.StatusForbidden, "sharing_refused", "Agent sharing (Bullpen) is not switched on for this server, so agents cannot join groups here yet. Ask your AgentWorks admin to turn it on.")
		return
	}
	if claims != nil && claims.UserID != "" && !userAllowedProduct(claims, "bullpen") {
		externalError(w, http.StatusForbidden, "sharing_refused", "Your account does not have agent sharing (Bullpen) on this server. Ask your AgentWorks admin to give you access.")
		return
	}
	if claims == nil || claims.AccessToken == nil || claims.AccessToken.BoundAgentID != "" || claims.UserID == "" || !namedAgentOperationAllowed(claims, true) {
		externalError(w, http.StatusForbidden, "sharing_refused", "Sharing needs your own CLI sign-in on this server: run agentworks login, then try again.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	var in struct {
		Action    string `json:"action"`
		AgentName string `json:"agent_name"`
		Group     string `json:"group"`
		Purpose   string `json:"purpose"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		externalError(w, http.StatusBadRequest, "invalid_arguments", "Send action, agent_name and group.")
		return
	}
	// Sharing an agent means joining a group, so share and unshare are join and leave.
	if (in.Action == "share" || in.Action == "unshare") && in.AgentName == "" {
		externalError(w, http.StatusBadRequest, "invalid_arguments", "Name one of your agents.")
		return
	}
	out, status, code, msg := api.groupAction(r.Context(), claims.UserID, in.Action, in.AgentName, in.Group, in.Purpose)
	if code != "" {
		externalError(w, status, code, msg)
		return
	}
	externalJSON(w, out)
}
