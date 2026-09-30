package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
)

// The deployment's own Google app (docs/design/google_accounts_gog.md): an admin stores one
// Google OAuth client for the server (the same app the MCP sign-in used), and people connect
// their own Google accounts through it. The account is private to one Code and its owner and
// is used through the server-side gog tool, which keeps the token off the agent's shell and
// enforces read-only. Nobody uploads a client_secret.json.

// platformGoogleApp is the stored Google app, if the admin set one up.
func platformGoogleApp() (clientID, clientSecret string, ok bool) {
	app, err := readMCPApp("google")
	if err != nil || app == nil || strings.TrimSpace(app.ClientID) == "" || strings.TrimSpace(app.ClientSecret) == "" {
		return "", "", false
	}
	return strings.TrimSpace(app.ClientID), strings.TrimSpace(app.ClientSecret), true
}

// gmailGoogleAppReturnURI is where Google sends the person back. It is the callback the
// deployment's Google client already registers for sign-ins (/api/oauth/callback), so using the
// platform app needs no new redirect URI in Google Cloud.
func gmailGoogleAppReturnURI(r *http.Request) string { return deriveOAuthRedirectURI(r) }

// GoogleAppRoutes wires the platform-app endpoints.
func GoogleAppRoutes(router *mux.Router, api *StreamingAPI) {
	router.HandleFunc("/api/human-feedback/gmail/google-app", googleAppStatusHandler(api)).Methods("GET")
	router.HandleFunc("/api/human-feedback/gmail/google-app/connect", googleAppConnectHandler(api)).Methods("POST", "OPTIONS")
}

func googleAppStatusHandler(api *StreamingAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, _, configured := platformGoogleApp()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"configured": configured, "redirect_uri": gmailGoogleAppReturnURI(r)})
	}
}

// GoogleAppConnectRequest creates one Google account connection through the platform app.
type GoogleAppConnectRequest struct {
	WorkspacePath string `json:"workspace_path"`
	DisplayName   string `json:"display_name,omitempty"`
	// Services are the Google Workspace services beyond Gmail (Drive, Calendar, Docs, Sheets,
	// Slides), each read-only unless Write is set. Read access to Gmail and the agent's
	// draft/send are separate switches; both default to off.
	Services              []services.GoogleServiceGrant `json:"services,omitempty"`
	AllowReadAccess       *bool                         `json:"allow_read_access,omitempty"`
	AllowAgentWriteAccess *bool                         `json:"allow_agent_write_access,omitempty"`
}

func googleAppConnectHandler(api *StreamingAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		var req GoogleAppConnectRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
			return
		}
		clientID, clientSecret, ok := platformGoogleApp()
		if !ok {
			http.Error(w, "This server has no Google app set up yet. Ask an administrator to add one.", http.StatusConflict)
			return
		}
		scope, scopeErr := gmailRequestScope(r, req.WorkspacePath)
		if scopeErr != nil {
			http.Error(w, scopeErr.Error(), http.StatusForbidden)
			return
		}
		// A person connects their own account to a Code they own; a shared (organisation)
		// account is an admin's.
		if scope.CodeWorkspace == "" && !currentUserIsAdmin(r) {
			writeWorkflowPermissionDenied(w, "admin")
			return
		}
		if _, err := services.EnsurePlatformOAuthClient(r.Context(), clientID, clientSecret); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// Who is asking is settled above; only now touch the Gmail service.
		svc, err := ensureGmailService()
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to initialize Gmail service: %v", err), http.StatusInternalServerError)
			return
		}
		displayName := googleAppDisplayName(req.DisplayName)
		conn, err := svc.CreateConnection(r.Context(), services.GmailConnectionInput{
			ScopeWorkspace:        scope.CodeWorkspace,
			OwnerID:               scope.UserID,
			DisplayName:           displayName,
			ClientName:            services.PlatformGoogleClientName,
			AllowReadAccess:       req.AllowReadAccess,
			AllowAgentWriteAccess: req.AllowAgentWriteAccess,
			Services:              req.Services,
			ServicesSet:           true,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusCreated)
		writeGmailConnection(w, r, svc, conn)
	}
}

// The account's email is only known after sign-in, so the connect form has no name
// field; a blank name gets a plain default instead of "display name is required".
func googleAppDisplayName(name string) string {
	if name = strings.TrimSpace(name); name != "" {
		return name
	}
	return "Google account"
}
