package server

import (
	"net/http"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productpolicy"
)

// Bullpen is an account directory, not an attachment or messaging authority.
// Browser sessions see the same account-owned names as the existing directory.
// Scoped CLI/MCP connections retain their existing, separately fenced tools.
func (api *StreamingAPI) handleBullpenAgents(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	if claims == nil || claims.UserID == "" {
		externalError(w, http.StatusUnauthorized, "unauthenticated", "Sign in to see your agents.")
		return
	}
	if claims.AccessToken != nil || claims.Provider == "bot_route" || !namedAgentOperationAllowed(claims, false) || !productpolicy.Enabled("bullpen") || !userAllowedProduct(claims, "bullpen") {
		externalError(w, http.StatusForbidden, "access_denied", "Bullpen is unavailable for this account or connection.")
		return
	}
	out, err := listNamedMessageAgents(r.Context(), claims.UserID)
	if err != nil {
		externalError(w, http.StatusServiceUnavailable, "directory_unavailable", "The agent directory is unavailable. Try again.")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	externalJSON(w, out)
}
