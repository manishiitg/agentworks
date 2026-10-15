package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

const googleAccountSharingNotice = "Anyone who can run or change this item can use this Google account through it. Removing it here leaves your other items connected."

func googleAccountActor(ctx context.Context) string {
	if claims := GetUserFromContext(ctx); claims != nil {
		if strings.TrimSpace(claims.UserID) == "" {
			return ""
		}
		return sanitizeUserIDForPath(claims.UserID)
	}
	if !IsMultiUserMode() {
		return sanitizeUserIDForPath(GetDefaultUserID())
	}
	return ""
}

func canManageGoogleAccount(r *http.Request, conn services.GmailConnection) bool {
	claims := GetUserFromContext(r.Context())
	access := userAccessForClaims(claims)
	if access.Disabled || IsMultiUserMode() && !access.Known {
		return false
	}
	if claims != nil && claims.Provider == "bot_route" {
		return false
	}
	if currentUserIsAdmin(r) {
		return true
	}
	return conn.IsPrivate() && googleAccountActor(r.Context()) != "" && googleAccountActor(r.Context()) == conn.OwnerID
}

// Both permission checks and revocation use the same physical item identity.
func googleAccountItemRoot(user, workspace string) string {
	workspace = canonicalCrewWorkspaceRoot(workspace)
	if root := common.CodeProjectRoot(user, workspace); root != "" {
		return root
	}
	kind, root := common.ClassifySessionWorkspace(user, workspace)
	if kind == common.SessionWorkspaceCrewProject {
		ref := workspaceref.MustParse(workspace)
		if _, shared := ref.SharedProject(); shared {
			return root
		}
		return ref.WithLogical(root).PhysicalKeepOwner(user)
	}
	return workspace
}

// googleItemScope resolves only the named item's root, and rechecks current
// product and Builder access. A missing workflow manifest is never authority.
func googleItemScope(r *http.Request, workspace string, write bool) (services.GmailUseScope, error) {
	user := googleAccountActor(r.Context())
	claims := GetUserFromContext(r.Context())
	scope := services.GmailUseScope{UserID: user}
	access := userAccessForClaims(claims)
	if access.Disabled || IsMultiUserMode() && !access.Known || write && !access.CanEdit {
		return scope, fmt.Errorf("Ask this item's Builder to manage Google accounts in Google apps.")
	}
	workspace = googleAccountItemRoot(user, workspace)
	if user == "" || workspace == "" || strings.Contains(workspace, "..") {
		return scope, fmt.Errorf("Sign in and open the item's Google apps to manage accounts.")
	}
	if claims != nil && claims.Provider == "bot_route" && write {
		return scope, fmt.Errorf("Ask this item's Builder to add a Google account in Google apps.")
	}
	if isProjectWorkspacePath(workspace) {
		product, _, _ := projectProductForPath(workspace)
		if !userAllowedProduct(claims, product.ProfileID) || !crewProjectOwnedByCaller(user, workspace) {
			return scope, fmt.Errorf("Only this item's Builder can manage its Google accounts. Open an item you build.")
		}
		workspace = agentProfileRuntimeWorkspace(user, workspace)
		if claims != nil && claims.AccessToken != nil {
			token := claims.AccessToken
			raw, found, err := readFileFromWorkspace(r.Context(), workspace+"/product.json")
			var metadata productProjectManifest
			if err != nil || !found || json.Unmarshal([]byte(raw), &metadata) != nil || metadata.ID == "" || !token.Allows("crews:write") || !token.AllowsCrew(metadata.ID) || token.FileGuard != nil {
				return scope, fmt.Errorf("Reconnect MCP with Builder access to this item, then add your Google account in Google apps.")
			}
		}
		if root := common.CodeProjectRoot(user, workspace); root != "" {
			workspace, scope.CodeWorkspace = root, root
		}
	} else {
		kind, root := common.ClassifySessionWorkspace(user, workspace)
		if kind != common.SessionWorkspaceWorkflow || root != workspace || strings.HasPrefix(root, "Workflow/.relay_releases") {
			return scope, fmt.Errorf("Open a workflow or Flow's Google apps to manage accounts.")
		}
		level, manifest := workflowAccessForWorkspacePath(r.Context(), claims, root)
		if manifest == nil || level == WorkflowAccessNone || write && level != WorkflowAccessOwner && level != WorkflowAccessWrite || !userAllowedWorkflowID(claims, manifest.ID) {
			return scope, fmt.Errorf("Ask this item's Builder to add a Google account in Google apps.")
		}
		if claims != nil && claims.AccessToken != nil {
			token := claims.AccessToken
			if !token.AllowsWorkflow(manifest.ID) || !token.Allows("builder:chat") || token.FileGuard != nil || manifest.Kind == "relay" && !token.Allows("relays:write") {
				return scope, fmt.Errorf("Reconnect MCP with Builder access to this item, then add your Google account in Google apps.")
			}
		}
	}
	scope.Workspace = workspace
	return scope, nil
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
	owner := googleAccountActor(r.Context()) == conn.OwnerID
	// Attaching always requires BOTH account ownership and current item Builder
	// access. Detaching also lets the account owner or admin revoke their grant.
	if selected && (!owner || accessErr != nil) {
		return fmt.Errorf("Add your own Google account to an item you build, from Google apps.")
	}
	if !selected && accessErr != nil && !owner && !currentUserIsAdmin(r) {
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
