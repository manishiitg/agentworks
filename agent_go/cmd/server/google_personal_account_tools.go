package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/gorilla/mux"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
)

type personalGoogleAccountRow struct {
	personalConnectionPermissions
	ID        string                  `json:"id"`
	Owner     string                  `json:"owner"`
	Label     string                  `json:"label"`
	Selected  bool                    `json:"selected"`
	Connected bool                    `json:"connected"`
	Mine      bool                    `json:"mine"`
	Google    GmailConnectionResponse `json:"google"`
}

// The website and Builder/MCP use the same saved records and operations.
func (api *StreamingAPI) personalGoogleOperation(r *http.Request, workspace string, args map[string]interface{}) (any, error) {
	action, _ := args["action"].(string)
	if action != "detach" {
		if err := personalAccountManagementScope(r, workspace, action == "connect"); err != nil {
			return nil, err
		}
	}
	svc, err := ensureGmailService()
	if err != nil {
		return nil, err
	}
	actor := personalConnectionActor(r.Context())
	id, _ := args["connection_id"].(string)
	switch action {
	case "list_saved":
		accounts := []personalGoogleAccountRow{}
		scope := gmailToolScope(workspace)
		scope.UserID = actor
		scope.Workspace = personalConnectionItemRoot(actor, workspace)
		for _, c := range svc.ListConnections() {
			selected := workspace != "" && c.UsableFrom(scope)
			if c.IsPrivate() && c.OwnerID != actor && !selected || !c.IsPrivate() && (workspace != "" || !currentUserIsAdmin(r)) {
				continue
			}
			conn := projectGmailConnection(r, svc, c, svc.GetConfig().DefaultConnectionID)
			permissions := personalConnectionAccess(r, c.OwnerID, workspace, selected, "Google", "Connections")
			if !c.IsPrivate() {
				permissions.CanManage, permissions.CanReconnect = conn.CanManage, conn.CanReconnect
			}
			accounts = append(accounts, personalGoogleAccountRow{personalConnectionPermissions: permissions, ID: c.ID, Owner: c.OwnerID, Label: conn.Email, Selected: selected, Connected: conn.Auth.Authenticated, Mine: c.OwnerID == actor, Google: conn})
		}
		result := map[string]any{"accounts": accounts, "can_connect": personalAccountManagementScope(r, workspace, true) == nil, "notice": googleAccountSharingNotice, "inbox_available": !IsLocalMode()}

		return result, nil
	case "attach", "detach":
		if err := selectGoogleAccount(r, id, workspace, action == "attach"); err != nil {
			return nil, err
		}
		return map[string]any{"selected": action == "attach", "notice": googleAccountSharingNotice}, nil
	case "connect":
		var input struct {
			GoogleAppConnectRequest
			ClientName string `json:"client_name"`
		}
		raw, _ := json.Marshal(args)
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, fmt.Errorf("Choose the Google services to connect.")
		}
		if input.ClientName == "" {
			clientID, clientSecret, ok := platformGoogleApp()
			if !ok {
				return nil, fmt.Errorf("Ask an administrator to set up Google sign-in, or choose your own Google app in My Vault.")
			}
			if _, err := services.EnsurePlatformOAuthClient(r.Context(), clientID, clientSecret); err != nil {
				return nil, err
			}
			input.ClientName = services.PlatformGoogleClientName
		}
		// Never accept credential paths, owner, or product selections from the body.
		create := services.GmailConnectionInput{Personal: true, OwnerID: actor, Workspaces: googleSelectedWorkspaces(personalConnectionItemRoot(actor, workspace)), DisplayName: googleAppDisplayName(input.DisplayName), ClientName: input.ClientName, AllowReadAccess: input.AllowReadAccess, AllowAgentWriteAccess: input.AllowAgentWriteAccess, Services: input.Services, ServicesSet: true}
		conn, err := svc.CreateOrReusePersonalGooglePending(r.Context(), create)
		if err != nil {
			return nil, err
		}
		id = conn.ID
	case "disconnect", "reconnect", "set_inbox_account":
	default:
		return nil, fmt.Errorf("Choose list_saved, connect, attach, detach, reconnect, disconnect or set_inbox_account.")
	}
	c, ok := svc.GetConnection(id)
	if !ok || !canManageGoogleAccount(r, c) {
		return nil, fmt.Errorf("Ask the account owner to manage this connection in My Vault.")
	}
	if action == "set_inbox_account" {
		if IsLocalMode() {
			return nil, fmt.Errorf("Crew inboxes are available on server deployments")
		}
		if !c.Personal || c.OwnerID != actor {
			return nil, fmt.Errorf("Choose a Gmail account you own. Ask me to list your saved accounts, or choose one in My Vault.")
		}
		status, _ := svc.AuthStatusForConnectionBlocking(r.Context(), c.ID)
		if !status.Authenticated || !services.GoogleScopesGrant(status.Scopes, services.GmailReadonlyScope) {
			return nil, fmt.Errorf("Use Change access to allow Gmail reading and complete Google sign-in first")
		}
		if err := svc.SetAgentInboxAccount(r.Context(), actor, id); err != nil {
			return nil, err
		}
		log.Printf("[GOOGLE_ACCOUNTS] actor=%s connection=%s reason=select_crew_inbox", actor, id)
		return map[string]any{"message": "This Gmail account supplies your Crew inboxes. Receiving is on by default on servers; Crews you turned off stay off. Open a Crew’s Inbox to copy its address. Ask Builder to turn receiving off or back on for that Crew. Receiving stores mail; Watchers run only when you enable them."}, nil
	}
	if action == "disconnect" {
		if err := svc.DeleteConnection(r.Context(), id); err != nil {
			return nil, err
		}
		log.Printf("[GOOGLE_ACCOUNTS] actor=%s connection=%s reason=disconnect_everywhere", actor, id)
		return map[string]any{"message": "Google account disconnected everywhere."}, nil
	}
	if c.IsPrivate() && c.OwnerID != actor {
		return nil, fmt.Errorf("Only the account owner can sign in again. Ask them to reconnect in My Vault.")
	}
	if action == "reconnect" {
		var update GoogleAppConnectRequest
		raw, _ := json.Marshal(args)
		if err := json.Unmarshal(raw, &update); err != nil {
			return nil, err
		}
		_, servicesSet := args["services"]
		c, err = svc.UpdateConnection(r.Context(), id, services.GmailConnectionInput{AllowReadAccess: update.AllowReadAccess, AllowAgentWriteAccess: update.AllowAgentWriteAccess, Services: update.Services, ServicesSet: servicesSet})
		if err != nil {
			return nil, err
		}
	}
	// Reuse the existing OAuth path, including local callbacks and client rotation.
	authRequest := r.Clone(r.Context())
	authRequest = mux.SetURLVars(authRequest, map[string]string{"id": c.ID})
	rec := httptest.NewRecorder()
	startGmailOAuthHandler(api)(rec, authRequest)
	if rec.Code != http.StatusOK {
		return nil, fmt.Errorf("%s", strings.TrimSpace(rec.Body.String()))
	}
	var started GmailOAuthStartResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &started); err != nil {
		return nil, err
	}
	auth := started.AuthURL
	log.Printf("[GOOGLE_ACCOUNTS] actor=%s connection=%s reason=%s", actor, id, action)
	return map[string]any{"connection_id": c.ID, "auth_url": auth, "next": "Finish signing in with Google, then return to My Vault. Use here adds this account to an item.", "notice": googleAccountSharingNotice}, nil
}

func (api *StreamingAPI) handlePersonalGoogleAccounts(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	args := map[string]interface{}{"action": "list_saved"}
	workspace := r.URL.Query().Get("workspace_path")
	if r.Method == http.MethodPost {
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&args) != nil {
			writeAgentProfileError(w, 400, "Choose a Google account and action.")
			return
		}
		workspace, _ = args["workspace_path"].(string)
	}
	result, err := api.personalGoogleOperation(r, workspace, args)
	if err != nil {
		writeAgentProfileError(w, 403, err.Error())
		return
	}
	writeAgentProfileJSON(w, 200, result)
}

func (api *StreamingAPI) registerPersonalGoogleAccountTool(reg definitionToolRegistrar, workspace string, callers ...*UserClaims) error {
	var caller *UserClaims
	if len(callers) > 0 {
		caller = callers[0]
	}
	return reg.RegisterCustomTool("manage_google_account",
		"Manage personal Google accounts saved in My Vault. list_saved shows reusable accounts and whether they are used here. connect saves your account and, when called from an item you build, explicitly adds it here, returning a Google sign-in link. attach/detach adds or removes the saved account here; disconnect removes it everywhere; reconnect signs in again and can update permissions. Website account management is in My Vault. Only the account owner signs in; existing Builder and account-management permissions apply. Personal Google connections use gog. On servers, set_inbox_account selects one of your own connected Gmail accounts with read access for Crew inboxes. Receiving is on by default on servers; selecting the account starts receiving for Crews unless explicitly turned off. The owner uses configure_agent_inbox to turn one Crew off or back on. Personal Gmail Watchers remain explicitly enabled. It does not lend general mailbox access to Crews or start work. Company accounts stay administrator-managed. "+googleAccountSharingNotice,
		map[string]interface{}{"type": "object", "properties": map[string]interface{}{
			"action":                   map[string]interface{}{"type": "string", "enum": []string{"list_saved", "connect", "attach", "detach", "disconnect", "reconnect", "set_inbox_account"}},
			"connection_id":            map[string]interface{}{"type": "string"},
			"client_name":              map[string]interface{}{"type": "string", "description": "Optional saved Google sign-in app; omit to use the company app."},
			"allow_read_access":        map[string]interface{}{"type": "boolean"},
			"allow_agent_write_access": map[string]interface{}{"type": "boolean"},
			"services":                 map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"service": map[string]interface{}{"type": "string", "enum": []string{"drive", "sheets", "docs", "slides", "calendar"}}, "write": map[string]interface{}{"type": "boolean"}}, "required": []string{"service"}}},
		}, "required": []string{"action"}}, func(ctx context.Context, args map[string]interface{}) (string, error) {
			// Native execution may omit the authenticated turn's context. Keep
			// that caller, including token restrictions; never infer an owner
			// from the workspace or the account being changed.
			if caller != nil {
				if current := GetUserFromContext(ctx); current != nil && current.UserID != caller.UserID {
					return "", fmt.Errorf("Open your own project chat to manage your Google accounts")
				}
				ctx = context.WithValue(ctx, UserContextKey, caller)
			}
			target := "/"
			if action, _ := args["action"].(string); action == "connect" || action == "reconnect" {
				target = strings.TrimRight(strings.TrimSpace(os.Getenv("PUBLIC_URL")), "/")
				if target == "" {
					return "", fmt.Errorf("Open My Vault on the website to finish Google sign-in.")
				}
			}
			req := httptest.NewRequest(http.MethodPost, target, nil).WithContext(ctx)
			result, err := api.personalGoogleOperation(req, workspace, args)
			if err != nil {
				return "", err
			}
			raw, err := json.Marshal(result)
			return string(raw), err
		}, "gmail_connection_management")
}
