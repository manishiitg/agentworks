package server

import (
	"net/http"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productpolicy"
)

// bullpenBrowserClaims admits only the signed-in person's own browser session.
// CLI and MCP tokens, bot routes and scoped or delegated principals are refused,
// so nothing an agent or a hook can run reaches a person-only Bullpen action.
func bullpenBrowserClaims(w http.ResponseWriter, r *http.Request, mutate bool) *UserClaims {
	claims := GetUserFromContext(r.Context())
	if claims == nil || claims.UserID == "" {
		externalError(w, http.StatusUnauthorized, "unauthenticated", "Sign in to see your agents.")
		return nil
	}
	if claims.AccessToken != nil || claims.Provider == "bot_route" || claims.Provider == "bot_owner" || !namedAgentOperationAllowed(claims, false) || !productpolicy.Enabled("bullpen") || !userAllowedProduct(claims, "bullpen") ||
		mutate && (claims.ExecutionPrincipal != nil || claims.Scope != "" || claims.BotRouteGrant != "" || claims.ExternalBuilderOperationID != "") {
		externalError(w, http.StatusForbidden, "access_denied", "Bullpen is unavailable for this account or connection.")
		return nil
	}
	return claims
}

// Bullpen is an account directory, not an attachment or messaging authority.
// Browser sessions see the same account-owned names as the existing directory.
// Scoped CLI/MCP connections retain their existing, separately fenced tools.
func (api *StreamingAPI) handleBullpenAgents(w http.ResponseWriter, r *http.Request) {
	claims := bullpenBrowserClaims(w, r, false)
	if claims == nil {
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
