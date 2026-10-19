package server

import (
	"net/http"
	"sort"
	"strings"
)

type workspaceNavigationItem struct {
	WorkspaceID   string   `json:"workspace_id"`
	ID            string   `json:"id"`
	ProfileID     string   `json:"profile_id"`
	Name          string   `json:"name"`
	WorkspacePath string   `json:"workspace_path"`
	Aliases       []string `json:"aliases,omitempty"`
}

// App-only detail of the existing workspace list. MCP/token list semantics and
// product settings/migrations remain unchanged; this response grants no access.
func (api *StreamingAPI) listWorkspaceNavigation(w http.ResponseWriter, r *http.Request, args map[string]any) {
	w.Header().Set("Cache-Control", "no-store")
	claims := GetUserFromContext(r.Context())
	if claims == nil || strings.TrimSpace(claims.UserID) == "" || claims.AccessToken != nil || claims.Scope != "" ||
		claims.ExecutionPrincipal != nil || claims.ExternalBuilderOperationID != "" || claims.BotRouteGrant != "" ||
		claims.Provider == "bot_owner" || claims.Provider == "bot_route" || claims.Provider == slackDMProvider || !externalWorkspaceAllowed(claims) {
		externalError(w, http.StatusForbidden, "forbidden", "Sign in to browse projects.")
		return
	}
	query := strings.ToLower(strings.TrimSpace(externalArg(args, "query")))
	if len(query) > 256 {
		externalError(w, http.StatusBadRequest, "invalid_arguments", "Project search is too long.")
		return
	}
	items := []workspaceNavigationItem{}
	seen := map[string]bool{}
	for _, product := range projectProducts {
		if !workspaceOriginAllowed(claims, product.ProfileID) {
			continue
		}
		for _, owner := range append([]string{claims.UserID}, crewProjectOwnerCandidates(claims.UserID)...) {
			rows, err := listWorkspaceProjectsForOwnerWithNavigation(r.Context(), claims.UserID, owner, query, product, true)
			if err != nil {
				externalError(w, http.StatusServiceUnavailable, "unavailable", "Projects could not be loaded. Try again.")
				return
			}
			for _, row := range rows {
				id, _ := row["id"].(string)
				path, _ := row["workspace_path"].(string)
				name, _ := row["name"].(string)
				if identity, ok := row["identity"].(accessibleProjectIdentity); ok && identity.Name != "" {
					name = identity.Name
				}
				key := product.ProfileID + ":" + id
				if id == "" || path == "" || seen[key] {
					continue
				}
				item := workspaceNavigationItem{WorkspaceID: key, ID: id, ProfileID: product.ProfileID, Name: name, WorkspacePath: path}
				if identity, ok := projectIdentityOf(path); ok {
					if record, registered := defaultProjectOwners().Lookup(identity.Product, identity.Folder); registered && record.ProjectID == id {
						for _, alias := range record.Aliases {
							item.Aliases = append(item.Aliases, canonicalChatHistoryWorkspacePath(claims.UserID, alias))
						}
					}
				}
				seen[key] = true
				items = append(items, item)
			}
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].WorkspaceID < items[j].WorkspaceID })
	externalJSON(w, map[string]any{"navigation_version": 1, "workspaces": items})
}
