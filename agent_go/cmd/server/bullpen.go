package server

import (
	"fmt"
	"net/http"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productpolicy"
)

// bullpenBrowserClaims admits only the signed-in person's own browser session.
// CLI and MCP tokens, bot routes and scoped or delegated principals are refused,
// so nothing an agent or a hook can run reaches a person-only AgentBridge action.
func bullpenBrowserClaims(w http.ResponseWriter, r *http.Request, mutate bool) *UserClaims {
	claims := GetUserFromContext(r.Context())
	if claims == nil || claims.UserID == "" {
		externalError(w, http.StatusUnauthorized, "unauthenticated", "Sign in to see your agents.")
		return nil
	}
	if claims.AccessToken != nil || claims.Provider == "bot_route" || claims.Provider == "bot_owner" || !namedAgentOperationAllowed(claims, false) || !productpolicy.Enabled("bullpen") || !userAllowedProduct(claims, "bullpen") ||
		mutate && (claims.ExecutionPrincipal != nil || claims.Scope != "" || claims.BotRouteGrant != "" || claims.ExternalBuilderOperationID != "") {
		externalError(w, http.StatusForbidden, "access_denied", "AgentBridge is unavailable for this account or connection.")
		return nil
	}
	return claims
}

// AgentBridge is an account directory, not an attachment or messaging authority.
// Browser sessions see the same account-owned names as the existing directory.
// Scoped CLI/MCP connections retain their existing, separately fenced tools.
func (api *StreamingAPI) handleBullpenAgents(w http.ResponseWriter, r *http.Request) {
	claims := bullpenBrowserClaims(w, r, false)
	if claims == nil {
		return
	}
	start := time.Now()
	s, err := readBullpenSnapshot(r)
	storeTime := time.Since(start)
	if err != nil {
		externalError(w, http.StatusServiceUnavailable, "directory_unavailable", "The agent directory is unavailable. Try again.")
		return
	}
	projectStart := time.Now()
	out := namedMessageAgentDirectory(r.Context(), claims.UserID, s, false)
	// The browser displays visibility and availability separately; delivery evidence stays in
	// presence and the receipt timestamps shown in agent details.
	for _, item := range out["agents"].([]map[string]interface{}) {
		label := "Only you"
		if item["published"] == true || item["shared"] == true {
			label = "Published"
		}
		availability := "Offline"
		if item["online"] == true {
			availability = "Online"
		} else if item["delivery_mode"] == "inbox" {
			availability = "On request"
		}
		item["visibility_label"] = label
		item["availability_label"] = availability
	}
	stampBullpenTiming(w, start, storeTime, "directory", time.Since(projectStart))
	w.Header().Set("Cache-Control", "no-store")
	externalJSON(w, out)
}

// Workspace documents are published by atomic rename. These metadata-only
// browser reads observe one complete snapshot; they neither authorize a message
// nor mutate a lease. Keep the global lock on all authorization/mutation paths.
func readBullpenSnapshot(r *http.Request) (agentMessageStore, error) {
	return readAgentMessageSnapshot(r.Context())
}

func stampBullpenTiming(w http.ResponseWriter, start time.Time, store time.Duration, phase string, work time.Duration) {
	w.Header().Set("Server-Timing", fmt.Sprintf("app;dur=%.2f, store;dur=%.2f, %s;dur=%.2f", float64(time.Since(start))/float64(time.Millisecond), float64(store)/float64(time.Millisecond), phase, float64(work)/float64(time.Millisecond)))
}

// Current usage is separate so its database cannot delay the agent directory.
// Only the signed-in owner receives it, and only for the current attachment.
func (api *StreamingAPI) handleBullpenAgentUsage(w http.ResponseWriter, r *http.Request) {
	claims := bullpenBrowserClaims(w, r, false)
	if claims == nil {
		return
	}
	start := time.Now()
	s, err := readBullpenSnapshot(r)
	storeTime := time.Since(start)
	if err != nil {
		externalError(w, http.StatusServiceUnavailable, "usage_unavailable", "Current usage is unavailable. Try again.")
		return
	}
	items := []map[string]interface{}{}
	for _, a := range s.Agents {
		if a.UserID == claims.UserID && a.Attachment != nil && namedAgentDiscoverable(a, time.Now().UTC()) {
			items = append(items, map[string]interface{}{"agent_id": a.ID})
		}
	}
	usageStart := time.Now()
	hydrateOwnerUsage(withAgentUsageTimezone(r.Context(), r.URL.Query().Get("timezone")), claims.UserID, items, s.Agents)
	stampBullpenTiming(w, start, storeTime, "usage", time.Since(usageStart))
	w.Header().Set("Cache-Control", "no-store")
	externalJSON(w, map[string]interface{}{"agents": items})
}
