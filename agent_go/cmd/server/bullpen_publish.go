package server

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// An agent is either connected (reachable only by its own person and that
// person's agents) or published to the network (every signed-in person on this
// server can find and message it). Publishing is one server-scope share; there
// are no groups.

// agentGroup and groupMember are the retired group records, read once by
// migrateGroupsToPublished and then dropped from the store.
type agentGroup struct {
	ID      string        `json:"group_id"`
	Members []groupMember `json:"members"`
}

type groupMember struct {
	AgentID string `json:"agent_id"`
	UserID  string `json:"user_id"`
}

func (s agentMessageStore) registeredAgent(agentID, userID string) bool {
	for _, a := range s.Agents {
		if a.ID == agentID && a.UserID == userID {
			return true
		}
	}
	return false
}

// migrateGroupsToPublished turns every member of an old group into a published
// agent and drops the groups. Undelivered mail sent inside a group is
// suppressed and the group conversations stop passing the share check, so they
// end with the plain "shared agent unavailable" notice; stored text is kept.
func (api *StreamingAPI) migrateGroupsToPublished(ctx context.Context) {
	agentMessageMu.Lock()
	defer agentMessageMu.Unlock()
	s, err := readAgentMessageStore(ctx)
	if err != nil || len(s.Groups) == 0 {
		return
	}
	now := time.Now().UTC()
	for _, g := range s.Groups {
		for _, m := range g.Members {
			if !s.registeredAgent(m.AgentID, m.UserID) || len(s.serverSharesOf(m.AgentID, now)) > 0 {
				continue
			}
			sh := agentShare{ID: "share-" + uuid.NewString(), OwnerUserID: m.UserID, AgentID: m.AgentID, SubjectUserID: shareScopeServer, Capability: shareCapabilityChat, CreatedAt: now, ExpiresAt: now.Add(serverShareLifetime), RevocationVersion: 1}
			s.Shares = append(s.Shares, sh)
			s.auditShare("group_published", m.UserID, m.UserID, shareScopeServer, sh.ID, m.AgentID)
		}
		s.suppressShare("group:" + g.ID)
	}
	s.Groups = nil
	_ = saveAgentMessageStore(ctx, s)
}

// publishAction runs share (publish) and unshare (unpublish) for one of the
// person's own agents. It is what the person's CLI login and the chat command
// reach; an older helper may still send a group name, which is ignored.
func (api *StreamingAPI) publishAction(ctx context.Context, ownerID, action, agentName string) (map[string]interface{}, int, string, string) {
	agent, err := normalizeMessageAgentName(agentName)
	if err != nil {
		return nil, http.StatusBadRequest, "invalid_arguments", "Name one of your agents."
	}
	switch action {
	case "share", "publish":
		out, status, code, msg := api.createShare(ctx, ownerID, agent, shareScopeServer, serverShareLifetime)
		if code == "already_shared" {
			return map[string]interface{}{"agent_name": agent, "state": "published"}, 0, "", ""
		}
		if code != "" {
			return nil, status, code, msg
		}
		out["state"] = "published"
		return out, 0, "", ""
	case "unshare", "unpublish":
		agentMessageMu.Lock()
		defer agentMessageMu.Unlock()
		s, err := readAgentMessageStore(ctx)
		if err != nil {
			return nil, http.StatusServiceUnavailable, "unavailable", "Publishing is unavailable. Try again."
		}
		for _, a := range s.Agents {
			if a.UserID != ownerID || a.Name != agent {
				continue
			}
			n := s.revokeServerShares(a.ID, ownerID, time.Now().UTC())
			if n > 0 {
				if err := saveAgentMessageStore(ctx, s); err != nil {
					return nil, http.StatusServiceUnavailable, "unavailable", "Publishing is unavailable. Try again."
				}
			}
			return map[string]interface{}{"agent_name": a.Name, "state": "unpublished", "was_published": n > 0}, 0, "", ""
		}
		return nil, http.StatusNotFound, "unknown_agent", "You have no agent with that name."
	}
	return nil, http.StatusBadRequest, "invalid_arguments", "Use share to publish an agent to the network, or unshare to take it back."
}
