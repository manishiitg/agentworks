package server

import (
	"context"
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
	if claims == nil || claims.AccessToken == nil || claims.AccessToken.BoundAgentID != "" || claims.UserID == "" ||
		!productpolicy.Enabled("bullpen") || !userAllowedProduct(claims, "bullpen") || !namedAgentOperationAllowed(claims, true) {
		externalError(w, http.StatusForbidden, "sharing_refused", "Sharing needs your own CLI sign-in on a server with Bullpen.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	var in struct {
		Action    string `json:"action"`
		AgentName string `json:"agent_name"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		externalError(w, http.StatusBadRequest, "invalid_arguments", "Send action and agent_name.")
		return
	}
	if in.Action == "list" {
		agentMessageMu.Lock()
		s, err := readAgentMessageStore(r.Context())
		agentMessageMu.Unlock()
		if err != nil {
			externalError(w, http.StatusServiceUnavailable, "unavailable", "Sharing is unavailable. Try again.")
			return
		}
		names := []string{}
		for _, a := range s.Agents {
			if a.UserID == claims.UserID && len(s.serverSharesOf(a.ID, time.Now().UTC())) > 0 {
				names = append(names, a.Name)
			}
		}
		externalJSON(w, map[string]interface{}{"shared_with_everyone": names})
		return
	}
	name, err := normalizeMessageAgentName(in.AgentName)
	if err != nil || (in.Action != "share" && in.Action != "unshare") {
		externalError(w, http.StatusBadRequest, "invalid_arguments", "action is share, unshare or list; agent_name is one of your registered agents.")
		return
	}
	var out map[string]interface{}
	var status int
	var code, msg string
	if in.Action == "share" {
		out, status, code, msg = api.createShare(r.Context(), claims.UserID, name, shareScopeServer, serverShareLifetime)
		if code == "already_shared" {
			out, code = map[string]interface{}{"agent_name": name, "scope": "everyone", "state": "active"}, ""
		}
	} else {
		out, status, code, msg = api.unshareEveryone(r.Context(), claims.UserID, name)
	}
	if code != "" {
		externalError(w, status, code, msg)
		return
	}
	out["scope"] = "everyone"
	externalJSON(w, out)
}

// unshareEveryone makes an agent private again. Existing conversations through
// the share stop at their next check.
func (api *StreamingAPI) unshareEveryone(ctx context.Context, owner, agentName string) (map[string]interface{}, int, string, string) {
	agentMessageMu.Lock()
	defer agentMessageMu.Unlock()
	s, err := readAgentMessageStore(ctx)
	if err != nil {
		return nil, http.StatusServiceUnavailable, "unavailable", "Sharing is unavailable. Try again."
	}
	for _, a := range s.Agents {
		if a.UserID != owner || a.Name != agentName {
			continue
		}
		if s.revokeServerShares(a.ID, owner, time.Now().UTC()) > 0 {
			if err := saveAgentMessageStore(ctx, s); err != nil {
				return nil, http.StatusServiceUnavailable, "unavailable", "Sharing is unavailable. Try again."
			}
		}
		return map[string]interface{}{"agent_name": a.Name, "state": "private"}, 0, "", ""
	}
	return nil, http.StatusNotFound, "unknown_agent", "You have no agent with that name."
}
