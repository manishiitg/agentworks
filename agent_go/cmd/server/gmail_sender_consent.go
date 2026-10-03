package server

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/gmailinbound"
)

func gmailOwnerEmail(route gmailinbound.Route) string {
	if user := directoryUserFor(route.OwnerID, "", ""); user != nil && user.Email != "" {
		return user.Email
	}
	if !IsMultiUserMode() {
		if svc := services.GetGmailService(); svc != nil {
			if conn, found := svc.GetConnection(route.ConnectionID); found {
				return conn.Email
			}
		}
	}
	return ""
}

// Approval is a login-session HTTP action, never an agent tool or an agent-
// supplied boolean. Bridge/PAT/CLI/MCP tokens and fabricated internal owner
// contexts cannot satisfy this check. AuthMiddleware still owns user admission.
func gmailSenderConsentLogin(r *http.Request) bool {
	claims := GetUserFromContext(r.Context())
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || claims == nil || claims.UserID == "" || claims.AccessToken != nil || claims.ExternalBuilderOperationID != "" || claims.ExecutionPrincipal != nil || claims.BotRouteGrant != "" || claims.Provider == "bot_route" || claims.Scope != "" {
		return false
	}
	var login UserClaims
	token, err := jwt.ParseWithClaims(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), &login, func(t *jwt.Token) (interface{}, error) {
		return GetAuthSecret(), nil
	}, jwt.WithValidMethods([]string{"HS256"}), jwt.WithIssuer("mcp-agent-builder"), jwt.WithExpirationRequired())
	if err != nil || !token.Valid || login.UserID == "" || login.Scope != "" || login.BotRouteGrant != "" || login.Provider == "bot_route" {
		return false
	}
	canonicalizeDirectoryUserClaims(&login)
	return login.UserID == claims.UserID
}

func (api *StreamingAPI) gmailSenderConsent(w http.ResponseWriter, r *http.Request) {
	if !gmailSenderConsentLogin(r) {
		http.Error(w, "Sender access can be confirmed only from the owner's signed-in browser session", http.StatusForbidden)
		return
	}
	if api.gmailInbound == nil {
		http.Error(w, "Gmail incoming email is unavailable", http.StatusServiceUnavailable)
		return
	}
	var input struct {
		WorkspacePath string `json:"workspace_path"`
		ConfigHash    string `json:"config_hash"`
		Action        string `json:"action"`
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, 8192))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || len(input.ConfigHash) != 64 || (input.Action != "approve" && input.Action != "revoke") {
		http.Error(w, "Invalid sender confirmation", http.StatusBadRequest)
		return
	}
	gmailTriggerConfigMu.Lock()
	defer gmailTriggerConfigMu.Unlock()
	owner := GetUserIDFromContext(r.Context())
	target, err := api.inboundTarget(r.Context(), owner, input.WorkspacePath)
	if err != nil {
		http.Error(w, "Only the current target owner can confirm email senders", http.StatusForbidden)
		return
	}
	routes, err := api.gmailInbound.Store.Routes(r.Context())
	if err != nil {
		http.Error(w, "Cannot read email configuration", http.StatusInternalServerError)
		return
	}
	for _, route := range routes {
		if route.OwnerID == owner && route.WorkspacePath == target.WorkspacePath && route.ProjectID == target.ProjectID && route.ProfileID == target.ProfileID {
			if err := api.gmailInbound.Store.ConfirmSenderConsent(r.Context(), owner, route.ID, input.ConfigHash, input.Action == "approve"); err != nil {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			writeAgentProfileJSON(w, http.StatusOK, map[string]bool{"approved": input.Action == "approve"})
			return
		}
	}
	http.Error(w, "Email trigger not found", http.StatusNotFound)
}
