package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
)

const googleAccountSharingNotice = "Anyone who can run or change this item can use this Google account through it. Removing it here leaves your other items connected."

func googleAccountActor(ctx context.Context) string { return personalConnectionActor(ctx) }
func canManageGoogleAccount(r *http.Request, conn services.GmailConnection) bool {
	if !conn.IsPrivate() && !currentUserIsAdmin(r) {
		return false
	}
	return canManagePersonalConnection(r, conn.OwnerID)
}
func googleAccountItemRoot(user, workspace string) string {
	return personalConnectionItemRoot(user, workspace)
}
func googleItemScope(r *http.Request, workspace string, write bool) (services.GmailUseScope, error) {
	scope, err := personalConnectionItemScope(r, workspace, write, "Google", "Google apps")
	return services.GmailUseScope{UserID: scope.UserID, Workspace: scope.Workspace, CodeWorkspace: scope.CodeWorkspace}, err
}

func selectGoogleAccount(r *http.Request, id, workspace string, selected bool) error {
	claims := GetUserFromContext(r.Context())
	access := userAccessForClaims(claims)
	if googleAccountActor(r.Context()) == "" || access.Disabled || IsMultiUserMode() && !access.Known || claims != nil && claims.Provider == "bot_route" {
		return fmt.Errorf("Sign in to Google apps to add or remove your account.")
	}
	svc, err := ensureGmailService()
	if err != nil {
		return err
	}
	conn, ok := svc.GetConnection(id)
	if !ok || !conn.IsPrivate() {
		return fmt.Errorf("Choose one of your saved personal Google accounts in Google apps.")
	}
	scope, accessErr := googleItemScope(r, workspace, true)
	permissions := personalConnectionAccess(r, conn.OwnerID, workspace, true, "Google", "Google apps")
	// Attaching always requires BOTH account ownership and current item Builder
	// access. Detaching also lets the account owner or admin revoke their grant.
	if selected && !permissions.CanAdd {
		return fmt.Errorf("Add your own Google account to an item you build, from Google apps.")
	}
	if !selected && !permissions.CanDetach {
		return accessErr
	}
	if accessErr != nil {
		scope.Workspace = googleAccountItemRoot(googleAccountActor(r.Context()), workspace)
	}
	if err := svc.SelectPersonalConnection(r.Context(), id, scope.Workspace, selected); err != nil {
		return err
	}
	log.Printf("[GOOGLE_ACCOUNTS] actor=%s connection=%s item=%s selected=%t reason=explicit_selection", googleAccountActor(r.Context()), id, scope.Workspace, selected)
	return nil
}

func selectGoogleAccountHandler(api *StreamingAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		var req struct {
			WorkspacePath string `json:"workspace_path"`
			Selected      bool   `json:"selected"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			http.Error(w, "Choose an item and whether to add or remove this account.", 400)
			return
		}
		if err := selectGoogleAccount(r, mux.Vars(r)["id"], req.WorkspacePath, req.Selected); err != nil {
			http.Error(w, err.Error(), 403)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"selected": req.Selected, "notice": googleAccountSharingNotice})
	}
}

func googleSelectedWorkspaces(workspace string) []string {
	if workspace == "" {
		return nil
	}
	return []string{workspace}
}
