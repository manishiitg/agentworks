package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

// Personal Google and Slack connections share the same current person, item
// identity and Builder/token rules. The saved account and its item selections
// are separate; selecting an account never changes a run's principal.
type personalConnectionScope struct{ UserID, Workspace, CodeWorkspace string }

func personalConnectionActor(ctx context.Context) string {
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

func canManagePersonalConnection(r *http.Request, owner string) bool {
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
	return personalConnectionActor(r.Context()) != "" && personalConnectionActor(r.Context()) == owner
}

// Both permission checks and revocation use the same physical item identity.
func personalConnectionItemRoot(user, workspace string) string {
	workspace = canonicalCrewWorkspaceRoot(workspace)
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

// personalConnectionItemScope resolves only the named item's root, and rechecks current
// product and Builder access. A missing workflow manifest is never authority.
func personalConnectionItemScope(r *http.Request, workspace string, write bool, provider, section string) (personalConnectionScope, error) {
	user := personalConnectionActor(r.Context())
	claims := GetUserFromContext(r.Context())
	scope := personalConnectionScope{UserID: user}
	access := userAccessForClaims(claims)
	if access.Disabled || IsMultiUserMode() && !access.Known || write && !access.CanEdit {
		return scope, fmt.Errorf("Ask this item's Builder to manage %s accounts in %s.", provider, section)
	}
	workspace = personalConnectionItemRoot(user, workspace)
	if user == "" || workspace == "" || strings.Contains(workspace, "..") {
		return scope, fmt.Errorf("Sign in and open the item's %s to manage accounts.", section)
	}
	if claims != nil && claims.Provider == "bot_route" && write {
		return scope, fmt.Errorf("Ask this item's Builder to add a %s account in %s.", provider, section)
	}
	if isProjectWorkspacePath(workspace) {
		product, _, _ := projectProductForPath(workspace)
		if !userAllowedProduct(claims, product.ProfileID) || !crewProjectOwnedByCaller(user, workspace) {
			return scope, fmt.Errorf("Only this item's Builder can manage its %s accounts. Open an item you build.", provider)
		}
		workspace = agentProfileRuntimeWorkspace(user, workspace)
		if claims != nil && claims.AccessToken != nil {
			token := claims.AccessToken
			raw, found, err := readFileFromWorkspace(r.Context(), workspace+"/product.json")
			var metadata productProjectManifest
			if err != nil || !found || json.Unmarshal([]byte(raw), &metadata) != nil || metadata.ID == "" || !token.Allows("crews:write") || !token.AllowsCrew(metadata.ID) || token.FileGuard != nil {
				return scope, fmt.Errorf("Reconnect MCP with Builder access to this item, then add your %s account in %s.", provider, section)
			}
		}
	} else {
		kind, root := common.ClassifySessionWorkspace(user, workspace)
		if kind != common.SessionWorkspaceWorkflow || root != workspace || strings.HasPrefix(root, "Workflow/.relay_releases") {
			return scope, fmt.Errorf("Open a workflow or Flow's %s to manage accounts.", section)
		}
		level, manifest := workflowAccessForWorkspacePath(r.Context(), claims, root)
		if manifest == nil || level == WorkflowAccessNone || write && level != WorkflowAccessOwner && level != WorkflowAccessWrite || !userAllowedWorkflowID(claims, manifest.ID) {
			return scope, fmt.Errorf("Ask this item's Builder to add a %s account in %s.", provider, section)
		}
		if claims != nil && claims.AccessToken != nil {
			token := claims.AccessToken
			if !token.AllowsWorkflow(manifest.ID) || !token.Allows("builder:chat") || token.FileGuard != nil || manifest.Kind == "relay" && !token.Allows("relays:write") {
				return scope, fmt.Errorf("Reconnect MCP with Builder access to this item, then add your %s account in %s.", provider, section)
			}
		}
	}
	scope.Workspace = workspace
	return scope, nil
}

// Personal account selection has the same policy for every provider.
type personalConnectionPermissions struct {
	CanAdd       bool `json:"can_add"`
	CanManage    bool `json:"can_manage"`
	CanReconnect bool `json:"can_reconnect"`
	CanDetach    bool `json:"can_detach"`
}

func personalConnectionAccess(r *http.Request, owner, workspace string, selected bool, provider, section string) personalConnectionPermissions {
	_, err := personalConnectionItemScope(r, workspace, true, provider, section)
	manage := canManagePersonalConnection(r, owner)
	return personalConnectionPermissions{CanAdd: err == nil && owner == personalConnectionActor(r.Context()), CanManage: manage, CanReconnect: manage && owner == personalConnectionActor(r.Context()), CanDetach: selected && (manage || err == nil)}
}

// A central account operation is personal, never a company or shared-vault grant.
// Item-bound MCP clients retain their existing Builder fence.
func personalAccountManagementScope(r *http.Request, workspace string, write bool) error {
	actor := personalConnectionActor(r.Context())
	if actor == "" || !canManagePersonalConnection(r, actor) {
		return fmt.Errorf("Sign in to manage your accounts in your project's Vault panel or in chat.")
	}
	if workspace != "" {
		_, err := personalConnectionItemScope(r, workspace, write, "personal", "Connections")
		return err
	}
	if claims := GetUserFromContext(r.Context()); claims != nil && claims.AccessToken != nil && (claims.AccessToken.FileGuard != nil || claims.AccessToken.ProjectID != "" || !claims.AccessToken.FullBuilderAccess()) {
		return fmt.Errorf("Open an item you build to manage its selected accounts, or use your project's Vault panel on the website.")
	}
	if write && !userAccessForClaims(GetUserFromContext(r.Context())).CanEdit {
		return fmt.Errorf("Ask an administrator for Builder access before connecting an account in your project's Vault panel or in chat.")
	}
	return nil
}
