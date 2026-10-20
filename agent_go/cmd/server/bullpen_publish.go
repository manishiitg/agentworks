package server

import (
	"context"
	"net/http"
	"time"
)

// An agent is either connected (reachable only by its own person and that
// person's agents) or published to the network (every signed-in person on this
// server can find and message it). Publishing is one server-scope share; there
// are no groups.

// oldHelperReply adds the fields a helper older than the network model prints.
func oldHelperReply(out map[string]interface{}, oldHelper bool, state string) map[string]interface{} {
	if !oldHelper {
		return out
	}
	out["state"], out["name"], out["group"] = state, "network", "network"
	if state == "left" {
		out["left"] = []string{"network"}
	}
	return out
}

// publishAction runs share (publish) and unshare (unpublish) for one of the
// person's own agents. It is what the person's CLI login and the chat command
// reach. An older helper still sends a group name: it is ignored, and the reply also uses the
// words that helper prints (joined/left "network") so it does not say nothing happened.
func (api *StreamingAPI) publishAction(ctx context.Context, ownerID, action, agentName string, oldHelper bool) (map[string]interface{}, int, string, string) {
	agent, err := normalizeMessageAgentName(agentName)
	if err != nil {
		return nil, http.StatusBadRequest, "invalid_arguments", "Name one of your agents."
	}
	switch action {
	case "share", "publish":
		out, status, code, msg := api.createShare(ctx, ownerID, agent, shareScopeServer, serverShareLifetime)
		if code == "already_shared" {
			return oldHelperReply(map[string]interface{}{"agent_name": agent, "state": "published"}, oldHelper, "joined"), 0, "", ""
		}
		if code != "" {
			return nil, status, code, msg
		}
		out["state"] = "published"
		return oldHelperReply(out, oldHelper, "joined"), 0, "", ""
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
			return oldHelperReply(map[string]interface{}{"agent_name": a.Name, "state": "unpublished", "was_published": n > 0}, oldHelper, "left"), 0, "", ""
		}
		return nil, http.StatusNotFound, "unknown_agent", "You have no agent with that name."
	}
	return nil, http.StatusBadRequest, "invalid_arguments", "Use share to publish an agent to the network, or unshare to take it back."
}
